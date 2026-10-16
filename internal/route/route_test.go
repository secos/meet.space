package route_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"meet.space/internal/middleware"
	"meet.space/internal/route"
	"meet.space/internal/site"
	_ "meet.space/internal/testkit/nonet"
)

const (
	public  = "localhost:8080"
	origin  = "origin.localhost:8080"
	hikers  = "hikers.localhost:8080"
	unknown = "unknown.localhost:8080"
)

var allHosts = []string{public, hikers, origin, unknown}

type harness struct {
	router   *route.Router
	ran      map[string]*atomic.Int32 // route key → handler calls
	notFound atomic.Int32
	logs     *strings.Builder
}

func ok(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body)
	})
}

func build(t *testing.T, routes []route.Route) *harness {
	t.Helper()
	return buildWithHostSource(t, routes, "host")
}

// buildWithHostSource builds the router with HOST_SOURCE set to "host" or
// the name of the header that carries the visitor's host.
func buildWithHostSource(t *testing.T, routes []route.Route, hostSource string) *harness {
	t.Helper()
	h := &harness{ran: map[string]*atomic.Int32{}, logs: &strings.Builder{}}
	for i := range routes {
		n := &atomic.Int32{}
		h.ran[routes[i].Key()] = n
		inner := routes[i].Handler
		routes[i].Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			inner.ServeHTTP(w, r)
		})
	}
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: uuid.Must(uuid.NewV7())})
	r, err := route.Build(route.Config{
		Resolver: &site.Resolver{PublicHost: public, OriginHost: origin, HostSource: hostSource, Domains: d},
		ClientIP: func(r *http.Request) netip.Addr { return middleware.ClientIP(r, nil, "X-Forwarded-For") },
		NotFound: func(w http.ResponseWriter, r *http.Request) {
			h.notFound.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "site-aware 404 for "+site.FromContext(r.Context()).Kind.String())
		},
		ServerError: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "site-aware 500")
		},
		Logger: slog.New(slog.NewTextHandler(writerFunc(func(p []byte) { h.logs.Write(p) }), nil)),
	}, routes)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	h.router = r
	return h
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

func (h *harness) do(method, host, path string, mod ...func(*http.Request)) *http.Response {
	r := httptest.NewRequest(method, "http://example.invalid"+path, nil)
	r.Host = host
	if method == http.MethodPost || method == http.MethodDelete {
		r.Header.Set("Origin", "http://"+host)
	}
	for _, m := range mod {
		m(r)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, r)
	return rec.Result()
}

func body(res *http.Response) string {
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func table() []route.Route {
	return []route.Route{
		{Method: "GET", Pattern: "/{$}", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.PublicPage, Handler: ok("home")},
		{Method: "GET", Pattern: "/static/{path...}", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.Immutable, Handler: ok("static")},
		{Method: "GET", Pattern: "/health", Hosts: route.OnOrigin, Role: route.Anyone, Cache: middleware.NoStore, Handler: ok("ok")},
		{Method: "GET", Pattern: "/directory", Hosts: route.OnPlatform, Role: route.Anyone, Cache: middleware.PublicPage, Handler: ok("directory")},
		{Method: "POST", Pattern: "/webhooks/stripe", Hosts: route.OnOrigin, Role: route.Signed, Cache: middleware.NoStore, OriginExempt: true, Handler: ok("hook")},
		{Method: "POST", Pattern: "/groups/{slug}/join", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.NoStore, Handler: ok("joined")},
		{Method: "GET", Pattern: "/rsvp/cancel/{token}", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.NoStore, TokenPage: true, Handler: ok("confirm")},
		{Method: "GET", Pattern: "/boom", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.NoStore, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })},
	}
}

// P0-ROUTES-01, -02, -07, P0-CACHE-05: every declaration is required, and
// the documented limits on caching, webhooks, token pages, the Origin-check
// exemption and platform-only routes are enforced when the table is built.
func TestRouteTable_RefusesBadDeclarations(t *testing.T) {
	good := route.Route{Method: "GET", Pattern: "/groups/{slug}", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.PublicPage, Handler: ok("")}
	if err := route.Validate(good); err != nil {
		t.Fatalf("good route refused: %v", err)
	}
	mut := func(f func(*route.Route)) route.Route { r := good; f(&r); return r }
	bad := map[string]route.Route{
		"no hosts":                    mut(func(r *route.Route) { r.Hosts = 0 }),
		"no role":                     mut(func(r *route.Route) { r.Role = route.RoleUndeclared }),
		"no cache policy":             mut(func(r *route.Route) { r.Cache = middleware.CacheUndeclared }),
		"no handler":                  mut(func(r *route.Route) { r.Handler = nil }),
		"PUT":                         mut(func(r *route.Route) { r.Method = "PUT" }),
		"PATCH":                       mut(func(r *route.Route) { r.Method = "PATCH" }),
		"cacheable POST":              mut(func(r *route.Route) { r.Method = "POST" }),
		"cacheable undocumented page": mut(func(r *route.Route) { r.Pattern = "/fragments/nav" }),
		"cacheable admin":             mut(func(r *route.Route) { r.Pattern = "/admin" }),
		"immutable page":              mut(func(r *route.Route) { r.Cache = middleware.Immutable }),
		"token route not flagged":     mut(func(r *route.Route) { r.Pattern = "/unsubscribe/{token}"; r.Cache = middleware.NoStore }),
		"flagged non-token route":     mut(func(r *route.Route) { r.TokenPage = true; r.Cache = middleware.NoStore }),
		"cacheable token page":        mut(func(r *route.Route) { r.Pattern = "/auth/verify/{token}"; r.TokenPage = true }),
		"Origin-exempt non-webhook":   mut(func(r *route.Route) { r.Cache = middleware.NoStore; r.OriginExempt = true }),
		"webhook on public host": {Method: "POST", Pattern: "/webhooks/ses", Hosts: route.OnPlatform | route.OnOrigin, Role: route.Signed,
			Cache: middleware.NoStore, OriginExempt: true, Handler: ok("")},
		"webhook not signed": {Method: "POST", Pattern: "/webhooks/ses", Hosts: route.OnOrigin, Role: route.Anyone,
			Cache: middleware.NoStore, OriginExempt: true, Handler: ok("")},
		"signed non-webhook":         mut(func(r *route.Route) { r.Role = route.Signed }),
		"directory on custom domain": mut(func(r *route.Route) { r.Pattern = "/directory" }),
		"platform admin on custom domain": {Method: "GET", Pattern: "/platform", Hosts: route.OnPlatform | route.OnGroup,
			Role: route.Anyone, Cache: middleware.NoStore, Handler: ok("")},
		"create form on custom domain": {Method: "GET", Pattern: "/admin/groups/new", Hosts: route.OnGroup,
			Role: route.Anyone, Cache: middleware.NoStore, Handler: ok("")},
		"create action on custom domain": {Method: "POST", Pattern: "/admin/groups", Hosts: route.OnGroup,
			Role: route.Anyone, Cache: middleware.NoStore, Handler: ok("")},
	}
	for name, r := range bad {
		if err := route.Validate(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A route can't be declared twice, and can't name a role nothing enforces
// yet (sessions arrive in Phase 1).
func TestRouteTable_BuildRefusesDuplicatesAndUnenforcedRoles(t *testing.T) {
	cfg := route.Config{Resolver: &site.Resolver{Domains: site.NewDomains(public, origin)}, ClientIP: func(*http.Request) netip.Addr { return netip.Addr{} },
		NotFound: func(http.ResponseWriter, *http.Request) {}, ServerError: func(http.ResponseWriter, *http.Request) {}}
	r := route.Route{Method: "GET", Pattern: "/{$}", Hosts: route.OnPlatform, Role: route.Anyone, Cache: middleware.PublicPage, Handler: ok("")}
	if _, err := route.Build(cfg, []route.Route{r, r}); err == nil {
		t.Error("duplicate accepted")
	}
	for _, role := range []route.Role{route.User, route.Member, route.Organizer, route.Owner, route.PlatformAdmin} {
		r.Role = role
		if _, err := route.Build(cfg, []route.Route{r}); err == nil {
			t.Errorf("role %s accepted without enforcement", role)
		}
	}
}

// P0-SITE-03 / decisions.md "Error pages": a host with no site gets the bare
// 404 on every method and path, no-store, and no handler runs.
func TestRouter_UnknownHostGetsBare404(t *testing.T) {
	h := build(t, table())
	for _, host := range []string{unknown, "localhost:8080.", "127.0.0.1:8080", ""} {
		for _, m := range []string{"GET", "POST", "DELETE", "HEAD"} {
			for _, p := range []string{"/", "/health", "/static/css/output.css", "/webhooks/stripe", "/x"} {
				res := h.do(m, host, p)
				b := body(res)
				if res.StatusCode != 404 || res.Header.Get("Cache-Control") != "no-store, private" {
					t.Errorf("%s %s%s: %d %q", m, host, p, res.StatusCode, res.Header.Get("Cache-Control"))
				}
				if m != "HEAD" && b != route.BareNotFoundBody {
					t.Errorf("%s %s%s: body %q", m, host, p, b)
				}
			}
		}
	}
	for k, n := range h.ran {
		if n.Load() != 0 {
			t.Errorf("%s ran for an unknown host", k)
		}
	}
	if h.notFound.Load() != 0 {
		t.Error("site-aware 404 used for an unknown host")
	}
}

// P0-ROUTES-05, -06, P0-SECURITY-08: host gating comes from the table; a
// route on a host kind it doesn't declare is a 404 and never runs. The
// origin host serves only /health and /webhooks/*, with the bare 404.
func TestRouter_HostGatingFromTable(t *testing.T) {
	h := build(t, table())
	type c struct {
		method, host, path string
		status             int
	}
	cases := []c{
		{"GET", public, "/", 200}, {"GET", hikers, "/", 200}, {"GET", origin, "/", 404},
		{"GET", public, "/directory", 200}, {"GET", hikers, "/directory", 404}, {"GET", origin, "/directory", 404},
		{"GET", origin, "/health", 200}, {"GET", public, "/health", 404}, {"GET", hikers, "/health", 404},
		{"POST", origin, "/webhooks/stripe", 200}, {"POST", public, "/webhooks/stripe", 404}, {"POST", hikers, "/webhooks/stripe", 404},
		{"GET", public, "/static/css/output.css", 200}, {"GET", hikers, "/static/x.js", 200}, {"GET", origin, "/static/x.js", 404},
		{"GET", origin, "/webhooks", 404}, {"GET", origin, "/webhooksx", 404}, {"GET", origin, "/healthz", 404},
	}
	for _, c := range cases {
		res := h.do(c.method, c.host, c.path)
		if res.StatusCode != c.status {
			t.Errorf("%s %s%s: %d, want %d", c.method, c.host, c.path, res.StatusCode, c.status)
		}
		if c.host == origin && c.status == 404 && body(res) != route.BareNotFoundBody {
			t.Errorf("%s %s%s: origin 404 isn't bare", c.method, c.host, c.path)
		}
	}
	if n := h.ran["GET /directory"].Load(); n != 1 {
		t.Errorf("/directory ran %d times, want only the platform request", n)
	}
}

// Critic addition: a path served for another method is a 404, never a 405
// with an Allow header that reveals the route.
func TestRouter_WrongMethodIs404Not405(t *testing.T) {
	h := build(t, table())
	for _, c := range [][3]string{{"POST", public, "/"}, {"GET", origin, "/webhooks/stripe"}, {"DELETE", public, "/directory"}, {"POST", hikers, "/directory"}} {
		res := h.do(c[0], c[1], c[2])
		if res.StatusCode != 404 || res.Header.Get("Allow") != "" {
			t.Errorf("%v: %d Allow=%q", c, res.StatusCode, res.Header.Get("Allow"))
		}
	}
}

// Critic addition: non-canonical paths never reach a handler or a redirect.
func TestRouter_NonCanonicalPathsAre404(t *testing.T) {
	h := build(t, table())
	for _, p := range []string{"//", "//directory", "/directory/", "/static", "/static/", "/static/../health", "/static/%2e%2e/health", "/./directory", "/static//x.js"} {
		for _, host := range []string{public, origin} {
			res := h.do("GET", host, p)
			if res.StatusCode != 404 || res.Header.Get("Location") != "" {
				t.Errorf("%s%s: %d Location=%q", host, p, res.StatusCode, res.Header.Get("Location"))
			}
		}
	}
	if n := h.ran["GET /health"].Load(); n != 0 {
		t.Errorf("/health reached through a dot segment %d times", n)
	}
}

// P0-SECURITY-01..07 at the router: the Origin check runs before the
// handler on every POST/DELETE that isn't exempt; webhooks are exempt; GET
// is never checked; a request refused earlier (unknown host, undeclared
// route) gets 404, not 403.
func TestRule5_RouterChecksOriginBeforeHandler(t *testing.T) {
	h := build(t, table())
	foreign := func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }
	none := func(r *http.Request) { r.Header.Del("Origin") }

	if res := h.do("POST", public, "/groups/x/join"); res.StatusCode != 200 {
		t.Errorf("same-origin POST: %d", res.StatusCode)
	}
	for name, mod := range map[string]func(*http.Request){"foreign": foreign, "missing": none} {
		res := h.do("POST", hikers, "/groups/x/join", mod)
		if res.StatusCode != 403 || body(res) != middleware.OriginRefusedBody || res.Header.Get("Cache-Control") != "no-store, private" {
			t.Errorf("%s origin: %d %q", name, res.StatusCode, res.Header.Get("Cache-Control"))
		}
	}
	if n := h.ran["POST /groups/{slug}/join"].Load(); n != 1 {
		t.Errorf("join handler ran %d times, want 1", n)
	}
	if !strings.Contains(h.logs.String(), "origin check refused request") {
		t.Error("refusal not logged")
	}
	// Webhooks are exempt (Rule 11).
	if res := h.do("POST", origin, "/webhooks/stripe", foreign); res.StatusCode != 200 {
		t.Errorf("webhook with foreign origin: %d", res.StatusCode)
	}
	if res := h.do("POST", origin, "/webhooks/stripe", none); res.StatusCode != 200 {
		t.Errorf("webhook without origin: %d", res.StatusCode)
	}
	// Earlier stages win.
	if res := h.do("POST", unknown, "/groups/x/join", foreign); res.StatusCode != 404 {
		t.Errorf("unknown host: %d", res.StatusCode)
	}
	if res := h.do("POST", public, "/nope", foreign); res.StatusCode != 404 {
		t.Errorf("undeclared route: %d", res.StatusCode)
	}
	// GET is not checked.
	if res := h.do("GET", public, "/", foreign); res.StatusCode != 200 {
		t.Errorf("GET with foreign origin: %d", res.StatusCode)
	}
}

// decisions.md "Origin check": DELETE is checked exactly like POST.
func TestRule5_RouterChecksOriginOnDelete(t *testing.T) {
	routes := append(table(), route.Route{Method: "DELETE", Pattern: "/groups/{slug}/rsvp", Hosts: route.OnPlatform | route.OnGroup,
		Role: route.Anyone, Cache: middleware.NoStore, Handler: ok("deleted")})
	h := build(t, routes)
	for _, host := range []string{public, hikers} {
		if res := h.do("DELETE", host, "/groups/x/rsvp"); res.StatusCode != 200 {
			t.Errorf("same-origin DELETE on %s: %d", host, res.StatusCode)
		}
		for name, mod := range map[string]func(*http.Request){
			"foreign": func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") },
			"missing": func(r *http.Request) { r.Header.Del("Origin") },
		} {
			res := h.do("DELETE", host, "/groups/x/rsvp", mod)
			if res.StatusCode != 403 || body(res) != middleware.OriginRefusedBody || res.Header.Get("Cache-Control") != "no-store, private" {
				t.Errorf("%s origin, DELETE on %s: %d %q", name, host, res.StatusCode, res.Header.Get("Cache-Control"))
			}
		}
	}
	if n := h.ran["DELETE /groups/{slug}/rsvp"].Load(); n != 2 {
		t.Errorf("DELETE handler ran %d times, want 2 (the same-origin requests)", n)
	}
}

// decisions.md "Origin check": the router compares with the host of the
// site the request resolved to, so each site accepts only its own origin;
// the platform, a custom domain and the origin host are strangers to each
// other.
func TestRule5_RouterComparesWithTheRequestsOwnSite(t *testing.T) {
	h := build(t, table())
	for _, host := range []string{public, hikers} {
		for _, from := range []string{public, hikers, origin} {
			res := h.do("POST", host, "/groups/x/join", func(r *http.Request) { r.Header.Set("Origin", "http://"+from) })
			want := 403
			if from == host {
				want = 200
			}
			if res.StatusCode != want {
				t.Errorf("POST %s with Origin http://%s: %d, want %d", host, from, res.StatusCode, want)
			}
		}
	}
}

// decisions.md "Origin check" and "HOST_SOURCE header edge cases": in
// header mode the Host header names the origin and the visitor's host
// comes from the configured header. The Origin check uses the resolved
// visitor host, never the Host header.
func TestRule5_RouterUsesResolvedHostInHeaderMode(t *testing.T) {
	h := buildWithHostSource(t, table(), "X-Visitor-Host")
	post := func(visitor, from string) *http.Response {
		return h.do("POST", origin, "/groups/x/join", func(r *http.Request) {
			r.Header.Set("X-Visitor-Host", visitor)
			r.Header.Set("Origin", from)
		})
	}
	for _, v := range []string{public, hikers} {
		if res := post(v, "http://"+v); res.StatusCode != 200 {
			t.Errorf("visitor %s with its own Origin: %d, want 200", v, res.StatusCode)
		}
		if res := post(v, "http://"+origin); res.StatusCode != 403 {
			t.Errorf("visitor %s with an Origin naming the Host header: %d, want 403", v, res.StatusCode)
		}
	}
}

// decisions.md "Origin check": only POST and DELETE are checked; HEAD, like
// GET, never is.
func TestRule5_RouterDoesNotCheckGetOrHead(t *testing.T) {
	h := build(t, table())
	for _, m := range []string{"GET", "HEAD"} {
		for name, mod := range map[string]func(*http.Request){
			"foreign": func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") },
			"missing": func(r *http.Request) { r.Header.Del("Origin") },
		} {
			if res := h.do(m, hikers, "/", mod); res.StatusCode != 200 {
				t.Errorf("%s with %s origin: %d", m, name, res.StatusCode)
			}
		}
	}
}

// decisions.md "Origin check": a refusal is logged as a warning with the
// host, method, path, Origin and Referer.
func TestRule5_RouterLogsRefusalDetails(t *testing.T) {
	h := build(t, table())
	h.do("POST", hikers, "/groups/x/join", func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.test")
		r.Header.Set("Referer", "https://evil.test/page")
	})
	log := h.logs.String()
	for _, want := range []string{"level=WARN", "origin check refused request", "host=" + hikers, "method=POST", "path=/groups/x/join",
		"origin=[https://evil.test]", "referer=[https://evil.test/page]"} {
		if !strings.Contains(log, want) {
			t.Errorf("log %q lacks %q", log, want)
		}
	}
}

// decisions.md "Security headers": every response the router sends carries
// nosniff and the framing ban, including 404s on every host kind, the
// origin-check refusal and a recovered panic.
func TestSecurityHeaders_OnEveryRouterResponse(t *testing.T) {
	h := build(t, table())
	cases := []struct {
		method, host, path string
		status             int
		mod                []func(*http.Request)
	}{
		{"GET", public, "/", 200, nil},
		{"GET", hikers, "/", 200, nil},
		{"GET", public, "/static/css/output.css", 200, nil},
		{"GET", origin, "/health", 200, nil},
		{"GET", public, "/rsvp/cancel/abc", 200, nil},
		{"POST", public, "/groups/x/join", 200, nil},
		{"GET", public, "/missing", 404, nil},
		{"GET", hikers, "/directory", 404, nil},
		{"GET", origin, "/", 404, nil},
		{"GET", unknown, "/", 404, nil},
		{"POST", hikers, "/", 404, nil},
		{"POST", public, "/groups/x/join", 403, []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }}},
		{"GET", public, "/boom", 500, nil},
		{"GET", unknown, "/boom", 404, nil},
	}
	for _, c := range cases {
		res := h.do(c.method, c.host, c.path, c.mod...)
		if res.StatusCode != c.status {
			t.Errorf("%s %s%s: status %d, want %d", c.method, c.host, c.path, res.StatusCode, c.status)
		}
		for k, v := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"Content-Security-Policy": "frame-ancestors 'none'",
			"X-Frame-Options":         "DENY",
		} {
			if got := res.Header.Values(k); len(got) != 1 || got[0] != v {
				t.Errorf("%s %s%s: %s %q, want %q", c.method, c.host, c.path, k, got, v)
			}
		}
	}
}

// P0-CACHE-01, -02, -06: the declared policy is applied; everything else,
// including 404s, panics and refusals, is no-store.
func TestRule7_RouterAppliesDeclaredCachePolicy(t *testing.T) {
	h := build(t, table())
	cases := []struct {
		method, host, path, want string
	}{
		{"GET", public, "/", "public, s-maxage=300"},
		{"GET", hikers, "/", "public, s-maxage=300"},
		{"GET", public, "/static/css/output.css", "public, max-age=31536000, immutable"},
		{"GET", origin, "/health", "no-store, private"},
		{"GET", public, "/missing", "no-store, private"},
		{"GET", public, "/boom", "no-store, private"},
		{"POST", public, "/groups/x/join", "no-store, private"},
		{"GET", public, "/rsvp/cancel/abc", "no-store, private"},
	}
	for _, c := range cases {
		res := h.do(c.method, c.host, c.path)
		if got := res.Header.Get("Cache-Control"); got != c.want {
			t.Errorf("%s %s%s: %q, want %q", c.method, c.host, c.path, got, c.want)
		}
	}
	res := h.do("GET", public, "/rsvp/cancel/abc")
	if got := res.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("token page Referrer-Policy %q", got)
	}
	res = h.do("GET", public, "/boom")
	if res.StatusCode != 500 || body(res) != "site-aware 500" {
		t.Errorf("panic: %d", res.StatusCode)
	}
}

// Security Rule 7 (decisions.md "Cookie on a cacheable response"): through
// the router, a cacheable route whose handler sets a cookie is sent
// no-store, and the error log names the host, method and path.
func TestRule7_RouterSendsCacheableRouteThatSetsCookieNoStore(t *testing.T) {
	routes := table()
	for i := range routes {
		if routes[i].Pattern == "/directory" {
			routes[i].Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
				fmt.Fprint(w, "directory")
			})
		}
	}
	h := build(t, routes)
	res := h.do("GET", public, "/directory")
	if got := res.Header.Get("Cache-Control"); got != "no-store, private" {
		t.Errorf("Cache-Control %q", got)
	}
	log := h.logs.String()
	for _, want := range []string{"level=ERROR", "cacheable response set a cookie", "host=" + public, "method=GET", "path=/directory"} {
		if !strings.Contains(log, want) {
			t.Errorf("log %q lacks %q", log, want)
		}
	}
	if res := h.do("GET", public, "/"); res.Header.Get("Cache-Control") != "public, s-maxage=300" {
		t.Errorf("cookie-free page: %q", res.Header.Get("Cache-Control"))
	}
}

// Security Rule 7 (cache.go: "everything not opted in gets NoStore"): a
// request that matches no route on its host is no-store because the router
// never opted it in, not only because its status happens to be 4xx. The
// not-found handler here answers 200 or 302, so the status override can't
// hide a router that defaults to, or leaks, a cacheable policy.
func TestRule7_UnmatchedRequestsAreNoStoreWhateverTheStatus(t *testing.T) {
	d := site.NewDomains(public, origin)
	d.Set(map[string]uuid.UUID{hikers: uuid.Must(uuid.NewV7())})
	for _, status := range []int{http.StatusOK, http.StatusFound} {
		r, err := route.Build(route.Config{
			Resolver: &site.Resolver{PublicHost: public, OriginHost: origin, HostSource: "host", Domains: d},
			ClientIP: func(r *http.Request) netip.Addr { return middleware.ClientIP(r, nil, "X-Forwarded-For") },
			NotFound: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/")
				w.WriteHeader(status)
			},
			ServerError: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, table())
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		h := &harness{router: r}
		for _, c := range []struct{ method, host, path string }{
			{"GET", public, "/missing"},   // no such route
			{"GET", hikers, "/directory"}, // a cacheable route not served on this host
			{"POST", public, "/"},         // a cacheable path, wrong method
		} {
			res := h.do(c.method, c.host, c.path)
			if res.StatusCode != status {
				t.Fatalf("%s %s%s: status %d, want the stub's %d", c.method, c.host, c.path, res.StatusCode, status)
			}
			if got := res.Header.Get("Cache-Control"); got != "no-store, private" {
				t.Errorf("%s %s%s (status %d): Cache-Control %q, want no-store, private", c.method, c.host, c.path, status, got)
			}
		}
	}
}

// The site-aware 404 serves the platform and custom domains.
func TestRouter_SiteAware404OnPlatformAndCustomDomain(t *testing.T) {
	h := build(t, table())
	for host, kind := range map[string]string{public: "platform", hikers: "group"} {
		res := h.do("GET", host, "/nope")
		if b := body(res); res.StatusCode != 404 || b != "site-aware 404 for "+kind {
			t.Errorf("%s: %d %q", host, res.StatusCode, b)
		}
	}
}

// P0-SITE-01: the site is resolved before the handler and available to it;
// the client IP is on the context.
func TestRouter_HandlerSeesSiteAndClientIP(t *testing.T) {
	var got site.Context
	var ip netip.Addr
	routes := []route.Route{{Method: "GET", Pattern: "/{$}", Hosts: route.OnPlatform | route.OnGroup, Role: route.Anyone, Cache: middleware.PublicPage,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = site.FromContext(r.Context())
			ip = middleware.ClientIPFrom(r.Context())
		})}}
	h := build(t, routes)
	h.do("GET", hikers, "/", func(r *http.Request) { r.RemoteAddr = "198.51.100.20:1234" })
	if got.Kind != site.KindGroup || got.Host != hikers {
		t.Errorf("site %+v", got)
	}
	if ip != netip.MustParseAddr("198.51.100.20") {
		t.Errorf("client IP %s", ip)
	}
}

// Security Rule 11: a webhook is declared on the origin host and nowhere
// else: every other combination of hosts, including any that adds the
// platform or a custom domain to the origin, is refused.
func TestRule11_WebhooksDeclaredOnlyOnOriginHost(t *testing.T) {
	for _, pattern := range []string{"/webhooks/stripe", "/webhooks/ses"} {
		for h := route.Hosts(1); h <= route.OnPlatform|route.OnGroup|route.OnOrigin; h++ {
			rt := route.Route{Method: "POST", Pattern: pattern, Hosts: h, Role: route.Signed,
				Cache: middleware.NoStore, OriginExempt: true, Handler: ok("")}
			err := route.Validate(rt)
			if h == route.OnOrigin && err != nil {
				t.Errorf("%s on %s refused: %v", pattern, h, err)
			}
			if h != route.OnOrigin && err == nil {
				t.Errorf("%s on %s accepted", pattern, h)
			}
		}
	}
}

// Rule 7: a handler that panics after a cacheable page has started must
// not leave a truncated page the CDN could keep; the connection is aborted.
func TestRule7_PanicAfterWritingAbortsTheResponse(t *testing.T) {
	routes := []route.Route{{Method: "GET", Pattern: "/{$}", Hosts: route.OnPlatform, Role: route.Anyone, Cache: middleware.PublicPage,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "<html>partial")
			w.(http.Flusher).Flush()
			panic("boom after writing")
		})}}
	h := build(t, routes)
	srv := httptest.NewServer(h.router)
	srv.Config.ErrorLog = slog.NewLogLogger(slog.NewTextHandler(io.Discard, nil), slog.LevelError)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = public
	res, err := srv.Client().Do(req)
	if err != nil {
		return // aborted before the headers: fine
	}
	defer res.Body.Close()
	if _, err := io.ReadAll(res.Body); err == nil {
		t.Fatalf("truncated page delivered as complete (status %d, Cache-Control %q)", res.StatusCode, res.Header.Get("Cache-Control"))
	}
}
