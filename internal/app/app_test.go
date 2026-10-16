package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"meet.space/internal/app"
	"meet.space/internal/handler"
	"meet.space/internal/middleware"
	"meet.space/internal/route"
	"meet.space/internal/site"
	"meet.space/internal/testkit/apptest"
)

var siteHosts = []string{apptest.PlatformHost, apptest.GroupHost}

// P0-ROUTES-03: at the end of Phase 0 the table holds exactly the home
// page, static files and /health, declared as documented.
func TestRouteTable_Phase0Routes(t *testing.T) {
	a := apptest.NewApp(t)
	type decl struct {
		hosts route.Hosts
		role  route.Role
		cache middleware.CachePolicy
	}
	want := map[string]decl{
		"GET /{$}":              {route.OnPlatform | route.OnGroup, route.Anyone, middleware.PublicPage},
		"GET /static/{path...}": {route.OnPlatform | route.OnGroup, route.Anyone, middleware.Immutable},
		"GET /health":           {route.OnOrigin, route.Anyone, middleware.NoStore},
	}
	got := a.App.Router.Routes()
	if len(got) != len(want) {
		t.Errorf("%d routes, want %d", len(got), len(want))
	}
	for _, r := range got {
		w, ok := want[r.Key()]
		if !ok {
			t.Errorf("unexpected route %s", r.Key())
			continue
		}
		if (decl{r.Hosts, r.Role, r.Cache}) != w {
			t.Errorf("%s declared %v/%v/%v, want %v", r.Key(), r.Hosts, r.Role, r.Cache, w)
		}
	}
}

// P0-SITE-13: the same path is a different page per host.
func TestHome_IsTheSitesOwnPage(t *testing.T) {
	a := apptest.NewApp(t)
	a.Seed()
	c := a.Client()
	p := c.Get(apptest.PlatformHost, "/")
	g := c.Get(apptest.GroupHost, "/")
	for _, r := range []*apptest.Response{p, g} {
		if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "public, s-maxage=300" {
			t.Errorf("status %d, Cache-Control %q", r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	if !strings.Contains(title(t, p.Body), apptest.PlatformName) {
		t.Errorf("platform title %q", title(t, p.Body))
	}
	if !strings.Contains(title(t, g.Body), apptest.HikersName) {
		t.Errorf("group title %q", title(t, g.Body))
	}
}

func title(t *testing.T, body string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var out string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			out = n.FirstChild.Data
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// urls returns every URL-bearing attribute in an HTML body.
func urls(t *testing.T, body string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, at := range n.Attr {
				switch at.Key {
				case "href", "src", "action", "hx-get", "hx-post", "hx-delete", "content", "srcset", "poster":
					out = append(out, at.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// P0-WHITELABEL-01, -03: on an active custom domain no response carries
// the platform's identity, and every URL stays on the visitor's host.
func TestWhiteLabel_CustomDomainShowsNoPlatformIdentity(t *testing.T) {
	a := apptest.NewApp(t)
	a.Seed()
	c := a.Client()
	paths := []string{"/", "/no-such-page", "/directory", "/admin/groups/new", "/health"}
	for _, p := range paths {
		for _, hx := range []bool{false, true} {
			var opts []apptest.ReqOption
			if hx {
				opts = append(opts, apptest.HX())
			}
			r := c.Get(apptest.GroupHost, p, opts...)
			all := r.Body
			for k, vs := range r.Header {
				all += k + ": " + strings.Join(vs, ",") + "\n"
			}
			for _, banned := range []string{apptest.PlatformName, apptest.PlatformHost, apptest.OriginHost} {
				if strings.Contains(strings.ToLower(all), strings.ToLower(banned)) {
					t.Errorf("GET %s (hx=%v): response contains %q", p, hx, banned)
				}
			}
			if strings.Contains(r.Header.Get("Content-Type"), "html") {
				for _, u := range urls(t, r.Body) {
					if strings.HasPrefix(u, "//") || (strings.Contains(u, "://") && !strings.HasPrefix(u, "http://"+apptest.GroupHost+"/")) {
						if strings.HasPrefix(u, "data:") {
							continue
						}
						t.Errorf("GET %s: URL %q leaves the custom domain", p, u)
					}
				}
			}
		}
	}
}

// decisions.md "Error pages": platform and custom domain 404s use their own
// layout with one message and one way out; hosts with no site, and the
// origin host, get the bare page; htmx requests get the message only.
func TestErrorPages_PerHostKind(t *testing.T) {
	a := apptest.NewApp(t)
	a.Seed()
	c := a.Client()
	for _, host := range siteHosts {
		r := c.Get(host, "/no-such-page")
		if r.StatusCode != 404 || r.Header.Get("Cache-Control") != "no-store, private" {
			t.Errorf("%s: %d %q", host, r.StatusCode, r.Header.Get("Cache-Control"))
		}
		if !strings.Contains(r.Body, handler.NotFoundMessage) && !strings.Contains(r.Body, "We can&#39;t find that page.") {
			t.Errorf("%s: 404 message missing", host)
		}
		if !slices.Contains(urls(t, r.Body), "/") {
			t.Errorf("%s: no way home", host)
		}
		name := apptest.PlatformName
		if host == apptest.GroupHost {
			name = apptest.HikersName
		}
		if !strings.Contains(title(t, r.Body)+r.Body, name) {
			t.Errorf("%s: not in the site's layout", host)
		}
		hx := c.Get(host, "/no-such-page", apptest.HX())
		if hx.StatusCode != 404 || strings.Contains(hx.Body, "<nav") || strings.Contains(hx.Body, "<html") {
			t.Errorf("%s: htmx 404 isn't fragment-sized: %q", host, hx.Body)
		}
	}
	for _, host := range []string{apptest.UnknownHost, apptest.PendingHost} {
		for _, p := range []string{"/", "/no-such-page", "/health"} {
			r := c.Get(host, p)
			if r.StatusCode != 404 || r.Body != route.BareNotFoundBody {
				t.Errorf("%s%s: %d %q", host, p, r.StatusCode, r.Body)
			}
		}
	}
	r := c.Get(apptest.OriginHost, "/")
	if r.StatusCode != 404 || r.Body != route.BareNotFoundBody {
		t.Errorf("origin /: %d %q", r.StatusCode, r.Body)
	}
}

// decisions.md "Error pages": the 500 is the site's own page with "Try
// again", no-store, and no error details.
func TestErrorPages_ServerError(t *testing.T) {
	a := apptest.NewApp(t)
	f := a.Seed()
	for _, sc := range []site.Context{
		{Kind: site.KindPlatform, Host: apptest.PlatformHost},
		{Kind: site.KindGroup, Host: apptest.GroupHost, GroupID: f.Hikers},
	} {
		req := httptest.NewRequest("GET", "http://x/some/page?q=1", nil)
		req = req.WithContext(site.NewContext(context.Background(), sc))
		rec := httptest.NewRecorder()
		pw := middleware.NewPolicyWriter(rec, middleware.PublicPage, false)
		a.App.Handlers.ServerError(pw, req)
		body := rec.Body.String()
		if rec.Code != 500 || rec.Header().Get("Cache-Control") != "no-store, private" {
			t.Errorf("%s: %d %q", sc.Kind, rec.Code, rec.Header().Get("Cache-Control"))
		}
		if !strings.Contains(body, "Something went wrong on our side.") || !slices.Contains(urls(t, body), "/some/page?q=1") {
			t.Errorf("%s: message or retry link missing", sc.Kind)
		}
		if sc.Kind == site.KindGroup && strings.Contains(strings.ToLower(body), apptest.PlatformName) {
			t.Errorf("group 500 names the platform")
		}
	}
}

// decisions.md "/health": origin host only; 200 "ok", or 503 when the
// database is unreachable, always no-store.
func TestHealth(t *testing.T) {
	a := apptest.NewApp(t)
	c := a.Client()
	r := c.Get(apptest.OriginHost, "/health")
	if r.StatusCode != 200 || r.Body != "ok\n" || r.Header.Get("Cache-Control") != "no-store, private" {
		t.Errorf("healthy: %d %q %q", r.StatusCode, r.Body, r.Header.Get("Cache-Control"))
	}
	for _, h := range []string{apptest.PlatformHost, apptest.GroupHost, apptest.UnknownHost} {
		if r := c.Get(h, "/health"); r.StatusCode != 404 {
			t.Errorf("%s/health: %d", h, r.StatusCode)
		}
	}
	a.DB.Pool.Close()
	r = c.Get(apptest.OriginHost, "/health")
	if r.StatusCode != 503 || r.Body != "database unavailable\n" || r.Header.Get("Cache-Control") != "no-store, private" {
		t.Errorf("unhealthy: %d %q", r.StatusCode, r.Body)
	}
}

var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// P0-CACHE-07, -08, -09 (Security Rule 7): with private data seeded, no
// cacheable page on either host contains an email or a virtual link, for
// any role; it sets no cookie; and it is the same for every role.
func TestRule7_CacheablePagesCarryNoPrivateData(t *testing.T) {
	a := apptest.NewApp(t)
	f := a.Seed()
	private := []string{f.RSVPEmail, f.VirtualURL}
	for _, e := range f.Emails {
		private = append(private, e)
	}
	for _, rt := range a.App.Router.Routes() {
		if rt.Cache != middleware.PublicPage {
			continue
		}
		path := samplePath(rt.Pattern)
		for _, host := range siteHosts {
			if k := map[string]site.Kind{apptest.PlatformHost: site.KindPlatform, apptest.GroupHost: site.KindGroup}[host]; !rt.Hosts.Has(k) {
				continue
			}
			var first string
			for _, role := range apptest.Roles {
				c := a.Client().As(a, f, role, host)
				r := c.Get(host, path)
				if r.Header.Get("Cache-Control") != "public, s-maxage=300" {
					t.Fatalf("%s%s is not cacheable here", host, path)
				}
				for _, p := range private {
					if strings.Contains(r.Body, p) {
						t.Errorf("%s%s as %s contains %q", host, path, role, p)
					}
				}
				if m := emailPattern.FindString(r.Body); m != "" {
					t.Errorf("%s%s as %s contains an email address %q", host, path, role, m)
				}
				if len(r.Cookies()) != 0 || r.Header.Get("Set-Cookie") != "" {
					t.Errorf("%s%s as %s sets a cookie", host, path, role)
				}
				if first == "" {
					first = r.Body
				} else if r.Body != first {
					t.Errorf("%s%s differs for role %s", host, path, role)
				}
			}
		}
	}
}

// P0-ROUTES-08: the role × host matrix, generated from the table. In Phase
// 0 every route is public, so every role gets the same status on a host;
// hosts a route doesn't declare give 404. Phase 1 adds the deny outcomes.
func TestRoleHostMatrix(t *testing.T) {
	a := apptest.NewApp(t)
	f := a.Seed()
	hostKinds := map[string]site.Kind{
		apptest.PlatformHost: site.KindPlatform, apptest.GroupHost: site.KindGroup,
		apptest.OriginHost: site.KindOrigin, apptest.UnknownHost: site.KindNone,
	}
	// The hosts each route is documented to be served on, written out by
	// hand so the matrix doesn't take its expectations from the table it
	// checks (development-plan.md "Site Context"; decisions.md "/health").
	documented := map[string][]site.Kind{
		"GET /{$}":              {site.KindPlatform, site.KindGroup},
		"GET /static/{path...}": {site.KindPlatform, site.KindGroup},
		"GET /health":           {site.KindOrigin},
	}
	for _, rt := range a.App.Router.Routes() {
		path := samplePath(rt.Pattern)
		served, ok := documented[rt.Key()]
		if !ok {
			t.Errorf("%s: add its documented hosts to this matrix", rt.Key())
			continue
		}
		for host, kind := range hostKinds {
			want := 404
			if slices.Contains(served, kind) {
				want = 200
			}
			for _, role := range apptest.Roles {
				c := a.Client().As(a, f, role, host)
				r := c.Do(rt.Method, host, path)
				if r.StatusCode != want {
					t.Errorf("%s %s%s as %s: %d, want %d", rt.Method, host, path, role, r.StatusCode, want)
				}
			}
		}
	}
}

// samplePath fills a pattern's wildcards with a value that exists.
func samplePath(pattern string) string {
	switch pattern {
	case "/{$}":
		return "/"
	case "/static/{path...}":
		return "/static/js/htmx.min.js"
	}
	return pattern
}

// P0-CACHE-03, -04: the page's assets are served from the visitor's own
// host, fingerprinted, immutable, with the right content types; they are
// not served on the origin host.
func TestStatic_AssetsServedFromTheVisitorsHost(t *testing.T) {
	a := apptest.NewApp(t)
	a.Seed()
	c := a.Client()
	for _, host := range siteHosts {
		page := c.Get(host, "/")
		var assets []string
		for _, u := range urls(t, page.Body) {
			if strings.HasPrefix(u, "/static/") {
				assets = append(assets, u)
			}
		}
		if len(assets) < 2 {
			t.Fatalf("%s: page references %d /static assets", host, len(assets))
		}
		for _, u := range assets {
			if !strings.Contains(u, "?v=") {
				t.Errorf("%s: %s isn't fingerprinted", host, u)
			}
			r := c.Get(host, u)
			if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Errorf("%s%s: %d %q", host, u, r.StatusCode, r.Header.Get("Cache-Control"))
			}
		}
		css := c.Get(host, "/static/css/output.css")
		if !strings.HasPrefix(css.Header.Get("Content-Type"), "text/css") {
			t.Errorf("css Content-Type %q", css.Header.Get("Content-Type"))
		}
		for _, p := range []string{"/static/no-such.css", "/static/fonts", "/static/../go.mod"} {
			if r := c.Get(host, p); r.StatusCode != 404 || r.Header.Get("Cache-Control") != "no-store, private" {
				t.Errorf("%s%s: %d %q", host, p, r.StatusCode, r.Header.Get("Cache-Control"))
			}
		}
	}
	if r := c.Get(apptest.OriginHost, "/static/js/htmx.min.js"); r.StatusCode != 404 {
		t.Errorf("origin serves static files: %d", r.StatusCode)
	}
}

// P0-HARNESS-17: tests build the app with the same constructor the server
// uses; it refuses to build without its dependencies.
func TestNew_RequiresDependencies(t *testing.T) {
	if _, err := app.New(app.Deps{}); err == nil {
		t.Fatal("app built without dependencies")
	}
	_ = http.MethodGet
}
