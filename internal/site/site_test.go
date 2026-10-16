package site_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"meet.space/internal/site"
	"meet.space/internal/testkit"
)

const (
	public  = "localhost:8080"
	origin  = "origin.localhost:8080"
	hikers  = "hikers.localhost:8080"
	pending = "pending.localhost:8080"
)

func resolver(d *site.Domains, source string) *site.Resolver {
	return &site.Resolver{PublicHost: public, OriginHost: origin, HostSource: source, Domains: d}
}

func req(host string, headers ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	r.Host = host
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	return r
}

// insertGroup adds a group with a custom domain in the given status.
func insertGroup(t *testing.T, db *testkit.DB, slug, domain, status string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	var d, s any
	if domain != "" {
		d, s = domain, status
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO groups (id, slug, name, timezone, custom_domain, custom_domain_status)
		VALUES ($1, $2, $2, 'America/Chicago', $3, $4)`, id, slug, d, s); err != nil {
		t.Fatal(err)
	}
	return id
}

// P0-SITE-02, -03, -13: each host maps to exactly one site kind, matched
// exactly (port included) after lowercasing; anything else is no site
// (decisions.md "Host matching").
func TestResolve_EachHostMapsToOneSite(t *testing.T) {
	g := uuid.Must(uuid.NewV7())
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: g})
	r := resolver(d, "host")
	cases := []struct {
		host string
		want site.Context
	}{
		{public, site.Context{Kind: site.KindPlatform, Host: public}},
		{"LOCALHOST:8080", site.Context{Kind: site.KindPlatform, Host: public}},
		{origin, site.Context{Kind: site.KindOrigin, Host: origin}},
		{hikers, site.Context{Kind: site.KindGroup, Host: hikers, GroupID: g}},
		{"Hikers.Localhost:8080", site.Context{Kind: site.KindGroup, Host: hikers, GroupID: g}},
		// No site:
		{"localhost:8080.", site.Context{}}, // trailing dot
		{"localhost", site.Context{}},       // port missing
		{"localhost:9090", site.Context{}},  // other port
		{"hikers.localhost:9090", site.Context{}},
		{"www.localhost:8080", site.Context{}},
		{"127.0.0.1:8080", site.Context{}},
		{"[::1]:8080", site.Context{}},
		{pending, site.Context{}},
		{"", site.Context{}},
	}
	for _, c := range cases {
		if got := r.Resolve(req(c.host)); got != c.want {
			t.Errorf("%q: got %+v, want %+v", c.host, got, c.want)
		}
	}
}

// P0-SITE-05 / decisions.md "HOST_SOURCE header edge cases": in host mode
// every host-ish header is ignored.
func TestResolve_HostModeIgnoresForwardedHostHeaders(t *testing.T) {
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: uuid.Must(uuid.NewV7())})
	r := resolver(d, "host")
	got := r.Resolve(req(public, "X-Forwarded-Host", hikers, "Forwarded", "host="+hikers, "X-Visitor-Host", hikers))
	if got.Kind != site.KindPlatform {
		t.Fatalf("spoofed headers changed the site: %+v", got)
	}
}

// In header mode: one value is used, a missing header falls back to Host,
// repeated, comma-separated or empty values are no site.
func TestResolve_HeaderModeEdgeCases(t *testing.T) {
	g := uuid.Must(uuid.NewV7())
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: g})
	r := resolver(d, "X-Visitor-Host")
	cases := []struct {
		name string
		r    *http.Request
		want site.Kind
	}{
		{"single value", req(origin, "X-Visitor-Host", "HIKERS.localhost:8080"), site.KindGroup},
		{"header wins over Host", req(origin, "X-Visitor-Host", public), site.KindPlatform},
		{"missing falls back to Host", req(origin), site.KindOrigin},
		{"repeated", req(origin, "X-Visitor-Host", public, "X-Visitor-Host", public), site.KindNone},
		{"comma-separated", req(origin, "X-Visitor-Host", public+", "+hikers), site.KindNone},
		{"empty", req(origin, "X-Visitor-Host", " "), site.KindNone},
		{"X-Forwarded-Host ignored", req(origin, "X-Forwarded-Host", public), site.KindOrigin},
	}
	for _, c := range cases {
		if got := r.Resolve(c.r).Kind; got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// P0-SITE-04, -06, -07, -09: the map holds active domains only, loaded from
// the database; a refresh adds, removes and changes without a restart; a
// suspended group's active domain still resolves.
func TestDomains_LoadAndRefreshFromDatabase(t *testing.T) {
	db := testkit.NewDB(t)
	ctx := context.Background()
	active := insertGroup(t, db, "hikers", hikers, "active")
	pend := insertGroup(t, db, "pending", pending, "pending_dns")
	insertGroup(t, db, "plain", "", "")
	if _, err := db.Pool.Exec(ctx, `UPDATE groups SET suspended_at = now(), tier = 'free' WHERE id = $1`, active); err != nil {
		t.Fatal(err)
	}
	d := site.NewDomains(public, origin)
	if err := d.Load(ctx, db.Pool); err != nil {
		t.Fatal(err)
	}
	r := resolver(d, "host")
	if got := r.Resolve(req(hikers)); got.Kind != site.KindGroup || got.GroupID != active {
		t.Errorf("active (suspended, free) domain: %+v", got)
	}
	if got := r.Resolve(req(pending)); got.Kind != site.KindNone {
		t.Errorf("pending_dns domain resolved: %+v", got)
	}

	// Activate the pending domain and deactivate the other; nothing changes
	// until a refresh.
	if _, err := db.Pool.Exec(ctx, `UPDATE groups SET custom_domain_status = 'active' WHERE id = $1`, pend); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE groups SET custom_domain = NULL, custom_domain_status = NULL WHERE id = $1`, active); err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(req(pending)); got.Kind != site.KindNone {
		t.Errorf("resolved before refresh: %+v", got)
	}
	if err := d.Load(ctx, db.Pool); err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(req(pending)); got.Kind != site.KindGroup || got.GroupID != pend {
		t.Errorf("newly active domain after refresh: %+v", got)
	}
	if got := r.Resolve(req(hikers)); got.Kind != site.KindNone {
		t.Errorf("removed domain still resolves: %+v", got)
	}

	// A deleted group's domain disappears on refresh.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, pend); err != nil {
		t.Fatal(err)
	}
	if err := d.Load(ctx, db.Pool); err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(req(pending)); got.Kind != site.KindNone {
		t.Errorf("deleted group's domain resolves: %+v", got)
	}
}

// Critic addition: PUBLIC_HOST and ORIGIN_HOST are matched first, so a
// groups row naming either can never capture it, nor get it from BaseURL.
func TestResolve_PlatformAndOriginHostsCantBeCaptured(t *testing.T) {
	db := testkit.NewDB(t)
	ctx := context.Background()
	a := insertGroup(t, db, "a", public, "active")
	b := insertGroup(t, db, "b", origin, "active")
	d := site.NewDomains(public, origin)
	if err := d.Load(ctx, db.Pool); err != nil {
		t.Fatal(err)
	}
	r := resolver(d, "host")
	if got := r.Resolve(req(public)).Kind; got != site.KindPlatform {
		t.Errorf("public host captured: %s", got)
	}
	if got := r.Resolve(req(origin)).Kind; got != site.KindOrigin {
		t.Errorf("origin host captured: %s", got)
	}
	l := links(d)
	for _, id := range []uuid.UUID{a, b} {
		if got := l.BaseURL(id); got != "http://localhost:8080" {
			t.Errorf("BaseURL for a group claiming a reserved host: %q", got)
		}
		if got := l.GroupHosts(id); len(got) != 1 {
			t.Errorf("GroupHosts for a group claiming a reserved host: %v", got)
		}
	}
}

// P0-SITE-08: lookups during refreshes are race-free and see a whole map.
func TestDomains_ConcurrentLookupsDuringRefresh(t *testing.T) {
	d := site.NewDomains(public, origin)
	g1, g2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m1 := map[string]uuid.UUID{"a.test": g1, "b.test": g1}
	m2 := map[string]uuid.UUID{"a.test": g2, "b.test": g2}
	d.Set(m1)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			if i%2 == 0 {
				d.Set(m2)
			} else {
				d.Set(m1)
			}
		}
		close(stop)
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				a, _ := d.Lookup("a.test")
				b, _ := d.Lookup("b.test")
				_ = a
				_ = b
			}
		}()
	}
	wg.Wait()
}

func links(d *site.Domains) site.Links {
	return site.Links{Scheme: "http", PublicBaseURL: "http://localhost:8080", PublicHost: public, Domains: d}
}

// P0-SITE-14 (Security Rule 2): links about a group use its custom domain
// only while the site cache holds it as active; otherwise the public base
// URL. The scheme follows PUBLIC_BASE_URL (decisions: dev uses http).
func TestRule2_BaseURLUsesOnlyActiveDomains(t *testing.T) {
	g, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: g})
	l := links(d)
	if got := l.BaseURL(g); got != "http://hikers.localhost:8080" {
		t.Errorf("active domain: %q", got)
	}
	if got := l.BaseURL(other); got != "http://localhost:8080" {
		t.Errorf("group without a domain: %q", got)
	}
	l.Scheme, l.PublicBaseURL = "https", "https://meet.example/"
	if got := l.BaseURL(g); got != "https://hikers.localhost:8080" {
		t.Errorf("https scheme: %q", got)
	}
	if got := l.BaseURL(other); got != "https://meet.example" {
		t.Errorf("trailing slash trimmed: %q", got)
	}
	d.Set(map[string]uuid.UUID{})
	if got := l.BaseURL(g); got != "https://meet.example" {
		t.Errorf("domain removed by refresh: %q", got)
	}
}

// P0-SITE-15 (Security Rule 2): a current-site base URL exists only for the
// platform and an active custom domain; never from a raw Host.
func TestRule2_CurrentBaseURLOnlyForPlatformAndActiveDomains(t *testing.T) {
	g := uuid.Must(uuid.NewV7())
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: g})
	l := links(d)
	if got, err := l.CurrentBaseURL(site.Context{Kind: site.KindPlatform, Host: public}); err != nil || got != "http://localhost:8080" {
		t.Errorf("platform: %q, %v", got, err)
	}
	if got, err := l.CurrentBaseURL(site.Context{Kind: site.KindGroup, Host: hikers, GroupID: g}); err != nil || got != "http://hikers.localhost:8080" {
		t.Errorf("group: %q, %v", got, err)
	}
	for _, c := range []site.Context{
		{Kind: site.KindOrigin, Host: origin},
		{},
		{Kind: site.KindGroup, Host: "evil.test", GroupID: g},                  // a context naming another host
		{Kind: site.KindGroup, Host: hikers, GroupID: uuid.Must(uuid.NewV7())}, // not this group's domain
	} {
		if got, err := l.CurrentBaseURL(c); err == nil {
			t.Errorf("%+v: got %q, want an error", c, got)
		}
	}
}

// P0-SITE-16: a group is served on the platform host, plus its custom
// domain while active.
func TestGroupHosts(t *testing.T) {
	g, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: g})
	l := links(d)
	if got := l.GroupHosts(g); len(got) != 2 || got[0] != public || got[1] != hikers {
		t.Errorf("active: %v", got)
	}
	if got := l.GroupHosts(other); len(got) != 1 || got[0] != public {
		t.Errorf("no domain: %v", got)
	}
}
