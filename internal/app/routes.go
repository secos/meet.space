package app

import (
	"net/http"

	"meet.space/internal/handler"
	"meet.space/internal/middleware"
	"meet.space/internal/route"
)

// Routes is the route table: the only place routes are declared. Each entry
// names its hosts, role and cache policy; route.Build refuses an entry
// that leaves one out.
func Routes(h *handler.Handlers, assets Assets) []route.Route {
	return []route.Route{
		// Platform: landing page. Custom domain: the group page.
		{Method: http.MethodGet, Pattern: "/{$}", Hosts: route.OnPlatform | route.OnGroup,
			Role: route.Anyone, Cache: middleware.PublicPage, Handler: http.HandlerFunc(h.Home)},

		// Fingerprinted assets, served on the visitor's own hostname so a
		// white-label domain never loads from anywhere else.
		{Method: http.MethodGet, Pattern: "/static/{path...}", Hosts: route.OnPlatform | route.OnGroup,
			Role: route.Anyone, Cache: middleware.Immutable, Handler: assets.Handler()},

		// Origin host only (decisions.md "/health").
		{Method: http.MethodGet, Pattern: "/health", Hosts: route.OnOrigin,
			Role: route.Anyone, Cache: middleware.NoStore, Handler: http.HandlerFunc(h.Health)},
	}
}
