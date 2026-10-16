// Package route holds the route table: every route declares the hosts that
// serve it, the role it needs and its cache policy, and the router is built
// from the table alone. A route can't be added without those declarations,
// and tests walk the table to check each one.
package route

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"path"
	"runtime/debug"
	"slices"
	"strings"

	"meet.space/internal/middleware"
	"meet.space/internal/site"
)

// Hosts is the set of site kinds that serve a route. Zero is undeclared.
type Hosts uint8

const (
	OnPlatform Hosts = 1 << iota
	OnGroup
	OnOrigin
)

// Has reports whether the route is served on a site of kind k.
func (h Hosts) Has(k site.Kind) bool {
	switch k {
	case site.KindPlatform:
		return h&OnPlatform != 0
	case site.KindGroup:
		return h&OnGroup != 0
	case site.KindOrigin:
		return h&OnOrigin != 0
	}
	return false
}

func (h Hosts) String() string {
	var parts []string
	if h&OnPlatform != 0 {
		parts = append(parts, "platform")
	}
	if h&OnGroup != 0 {
		parts = append(parts, "group")
	}
	if h&OnOrigin != 0 {
		parts = append(parts, "origin")
	}
	if len(parts) == 0 {
		return "undeclared"
	}
	return strings.Join(parts, "+")
}

// Role is who may call a route. Zero is undeclared.
type Role uint8

const (
	RoleUndeclared Role = iota
	// Anyone, logged in or not.
	Anyone
	// Signed is for webhooks: the handler verifies the sender's signature
	// before doing anything (Security Rule 11).
	Signed
	// User needs a session on this host.
	User
	// Member of the route's group.
	Member
	// Organizer is an owner or co-organizer of the route's group.
	Organizer
	// Owner of the route's group.
	Owner
	// PlatformAdmin is users.is_platform_admin.
	PlatformAdmin
)

func (r Role) String() string {
	return [...]string{"undeclared", "anyone", "signed", "user", "member", "organizer", "owner", "platform-admin"}[r]
}

// Route is one entry of the table.
type Route struct {
	Method  string // GET, POST or DELETE (HEAD is served by GET)
	Pattern string // net/http pattern path, e.g. "/groups/{slug}"
	Hosts   Hosts
	Role    Role
	Cache   middleware.CachePolicy
	// OriginExempt skips the Origin check. Only webhooks may set it.
	OriginExempt bool
	// TokenPage marks a route with a secret token in its path; it gets the
	// token-page Referrer-Policy. Required for every {token} route.
	TokenPage bool
	Handler   http.Handler
}

// Key is the method and pattern, as registered on the mux.
func (r Route) Key() string { return r.Method + " " + r.Pattern }

// Documented cache opt-ins (development-plan.md "Cacheable Pages" and
// "Files"). No other route may be cached.
var (
	publicPagePatterns = []string{"/{$}", "/directory", "/groups/{slug}", "/groups/{slug}/meetings/{id}", "/terms", "/privacy"}
	immutablePatterns  = []string{"/static/{path...}", "/files/{group_id}/{name}"}
	// Platform-only routes (404 on custom domains).
	platformOnlyPrefixes = []string{"/directory", "/admin/groups/new", "/platform"}
)

// Validate checks one route's declarations against the documented rules.
func Validate(rt Route) error {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{rt.Key()}, a...)...))
	}
	switch rt.Method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
	default:
		bad("method must be GET, POST or DELETE")
	}
	if !strings.HasPrefix(rt.Pattern, "/") {
		bad("pattern must be a path starting with /")
	}
	if rt.Hosts == 0 || rt.Hosts&^(OnPlatform|OnGroup|OnOrigin) != 0 {
		bad("hosts undeclared")
	}
	if rt.Role == RoleUndeclared || rt.Role > PlatformAdmin {
		bad("role undeclared")
	}
	if rt.Cache == middleware.CacheUndeclared || rt.Cache > middleware.Immutable {
		bad("cache policy undeclared")
	}
	if rt.Handler == nil {
		bad("no handler")
	}
	if rt.Cache == middleware.PublicPage && (rt.Method != http.MethodGet || !slices.Contains(publicPagePatterns, rt.Pattern)) {
		bad("only the documented GET pages may be cacheable")
	}
	if rt.Cache == middleware.Immutable && (rt.Method != http.MethodGet || !slices.Contains(immutablePatterns, rt.Pattern)) {
		bad("only /static and /files may be immutable")
	}
	hasToken := strings.Contains(rt.Pattern, "{token}")
	if hasToken != rt.TokenPage {
		bad("routes with a {token} segment, and only those, are token pages")
	}
	if rt.TokenPage && rt.Cache != middleware.NoStore {
		bad("token pages must be no-store")
	}
	webhook := strings.HasPrefix(rt.Pattern, "/webhooks/")
	if webhook && rt.Hosts != OnOrigin {
		bad("webhooks are served only on the origin host (Rule 11)")
	}
	if rt.OriginExempt && !webhook {
		bad("only webhooks are exempt from the Origin check")
	}
	if (rt.Role == Signed) != webhook {
		bad("webhooks, and only webhooks, use the signed role")
	}
	if rt.Hosts&OnGroup != 0 {
		for _, p := range platformOnlyPrefixes {
			if rt.Pattern == p || strings.HasPrefix(rt.Pattern, p+"/") {
				bad("platform-only route declared on custom domains")
			}
		}
		if rt.Method == http.MethodPost && rt.Pattern == "/admin/groups" {
			bad("platform-only route declared on custom domains")
		}
	}
	return errors.Join(errs...)
}

// Config is what the router needs besides the table.
type Config struct {
	Resolver *site.Resolver
	// ClientIP extracts the visitor's address (Security Rule 8).
	ClientIP func(*http.Request) netip.Addr
	// NotFound writes the site-aware 404 for the platform and custom
	// domains. ServerError writes the site-aware 500.
	NotFound    func(http.ResponseWriter, *http.Request)
	ServerError func(http.ResponseWriter, *http.Request)
	Logger      *slog.Logger
}

// Router is the app's http.Handler, built from the table.
type Router struct {
	cfg       Config
	mux       *http.ServeMux
	byPattern map[string]Route
	routes    []Route
}

// Build validates every route and registers it. Roles that need a session
// can't be declared until the session layer exists (Phase 1), so a route
// can never be declared with a role nothing enforces.
func Build(cfg Config, routes []Route) (*Router, error) {
	if cfg.Resolver == nil || cfg.NotFound == nil || cfg.ServerError == nil || cfg.ClientIP == nil {
		return nil, errors.New("route: incomplete config")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	r := &Router{cfg: cfg, mux: http.NewServeMux(), byPattern: map[string]Route{}}
	var errs []error
	for _, rt := range routes {
		if err := Validate(rt); err != nil {
			errs = append(errs, err)
			continue
		}
		if rt.Role != Anyone && rt.Role != Signed {
			errs = append(errs, fmt.Errorf("%s: role %s is not enforced yet", rt.Key(), rt.Role))
			continue
		}
		if _, dup := r.byPattern[rt.Key()]; dup {
			errs = append(errs, fmt.Errorf("%s: declared twice", rt.Key()))
			continue
		}
		r.byPattern[rt.Key()] = rt
		r.routes = append(r.routes, rt)
		r.mux.Handle(rt.Key(), r.dispatch(rt))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return r, nil
}

// Routes returns the table the router was built from.
func (r *Router) Routes() []Route { return slices.Clone(r.routes) }

// ServeHTTP runs every request through, in order: site resolution, the
// path and route-table host checks, the cache policy, the Origin check,
// client IP, then the handler. A request refused at one stage never
// reaches the next.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	sc := r.cfg.Resolver.Resolve(req)
	req = req.WithContext(site.NewContext(req.Context(), sc))
	pw := middleware.NewPolicyWriter(w, middleware.NoStore, false)
	defer r.recoverPanic(pw, req)

	if sc.Kind == site.KindNone {
		WriteBareNotFound(pw)
		return
	}
	rt, ok := r.match(req)
	if !ok || !rt.Hosts.Has(sc.Kind) {
		r.notFound(pw, req, sc)
		return
	}
	pw.Policy, pw.TokenPage = rt.Cache, rt.TokenPage
	if (req.Method == http.MethodPost || req.Method == http.MethodDelete) && !rt.OriginExempt {
		if err := middleware.CheckOrigin(req, sc.Host); err != nil {
			r.cfg.Logger.Warn("origin check refused request",
				"host", sc.Host, "method", req.Method, "path", req.URL.Path,
				"origin", req.Header.Values("Origin"), "referer", req.Header.Values("Referer"))
			pw.Policy = middleware.NoStore
			middleware.WriteOriginRefused(pw)
			return
		}
	}
	req = req.WithContext(middleware.WithClientIP(req.Context(), r.cfg.ClientIP(req)))
	r.mux.ServeHTTP(pw, req)
}

// match finds the table entry for a request. Non-canonical paths (dot
// segments, doubled or trailing slashes) match nothing, so ServeMux never
// answers with its own redirect, and a method mismatch is a 404, not a 405.
func (r *Router) match(req *http.Request) (Route, bool) {
	p := req.URL.Path
	if p == "" || p[0] != '/' || path.Clean(p) != p {
		return Route{}, false
	}
	_, pattern := r.mux.Handler(req)
	if pattern == "" {
		return Route{}, false
	}
	rt, ok := r.byPattern[pattern]
	if !ok {
		return Route{}, false
	}
	// For a "{name...}" pattern ServeMux answers the bare prefix ("/static")
	// with a redirect to "/static/"; treat that as no match instead.
	if i := strings.Index(rt.Pattern, "{"); i >= 0 && strings.HasSuffix(rt.Pattern, "...}") {
		prefix := rt.Pattern[:i]
		if !strings.HasPrefix(p, prefix) || len(p) == len(prefix) {
			return Route{}, false
		}
	}
	return rt, true
}

// dispatch wraps a route's handler. It refuses to run if the mux matched a
// different pattern than the table entry, which would mean the table and
// the mux disagree.
func (r *Router) dispatch(rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Pattern != rt.Key() {
			r.cfg.Logger.Error("route: mux pattern does not match table", "got", req.Pattern, "want", rt.Key())
			r.cfg.ServerError(w, req)
			return
		}
		rt.Handler.ServeHTTP(w, req)
	})
}

func (r *Router) notFound(w http.ResponseWriter, req *http.Request, sc site.Context) {
	if sc.Kind == site.KindOrigin {
		WriteBareNotFound(w)
		return
	}
	r.cfg.NotFound(w, req)
}

func (r *Router) recoverPanic(pw *middleware.PolicyWriter, req *http.Request) {
	v := recover()
	if v == nil {
		return
	}
	if v == http.ErrAbortHandler {
		panic(v)
	}
	r.cfg.Logger.Error("panic serving request", "path", req.URL.Path, "panic", v, "stack", string(debug.Stack()))
	if pw.Status() != 0 {
		// Too late to send an error page. Abort the connection so the client
		// and the CDN see a broken response, never a cacheable truncated page.
		panic(http.ErrAbortHandler)
	}
	pw.Policy = middleware.NoStore
	if k := site.FromContext(req.Context()).Kind; k == site.KindNone || k == site.KindOrigin {
		http.Error(pw, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	r.cfg.ServerError(pw, req)
}

// BareNotFoundBody is the 404 for hosts with no site: no name, no layout,
// no link (decisions.md "Error pages").
const BareNotFoundBody = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Page not found</title></head>
<body><p>Page not found.</p></body></html>
`

// WriteBareNotFound sends the bare 404.
func WriteBareNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(BareNotFoundBody))
}
