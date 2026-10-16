// Package app assembles the server's HTTP handler. cmd/server and the test
// harness both build it here, with the same middleware order; tests pass
// fakes, a fake clock and a test database.
package app

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"

	"meet.space/internal/clock"
	"meet.space/internal/config"
	"meet.space/internal/handler"
	"meet.space/internal/middleware"
	"meet.space/internal/route"
	"meet.space/internal/site"
)

// Assets serves and fingerprints files under /static (internal/assets).
type Assets interface {
	Handler() http.Handler
	URL(name string) string
}

// Deps is everything the app needs from outside.
type Deps struct {
	Config   config.Config
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Domains  *site.Domains
	Renderer handler.Renderer
	Assets   Assets
	Logger   *slog.Logger
}

// App is the assembled server.
type App struct {
	Handler  http.Handler
	Router   *route.Router
	Resolver *site.Resolver
	Links    site.Links
	Handlers *handler.Handlers
	// ClientIP is the Security Rule 8 extractor the router puts on every
	// request's context, built from TRUSTED_PROXIES and CLIENT_IP_HEADER.
	ClientIP func(*http.Request) netip.Addr
}

// New builds the app.
func New(d Deps) (*App, error) {
	if d.Pool == nil || d.Clock == nil || d.Domains == nil || d.Renderer == nil || d.Assets == nil {
		return nil, errors.New("app: missing dependency")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	trusted, err := d.Config.TrustedProxyPrefixes()
	if err != nil {
		return nil, err
	}
	resolver := &site.Resolver{
		PublicHost: d.Config.PublicHost,
		OriginHost: d.Config.OriginHost,
		HostSource: d.Config.HostSource,
		Domains:    d.Domains,
	}
	links := site.Links{
		Scheme:        d.Config.Scheme(),
		PublicBaseURL: d.Config.PublicBaseURL,
		PublicHost:    d.Config.PublicHost,
		Domains:       d.Domains,
	}
	h := &handler.Handlers{
		PlatformName: d.Config.PlatformName,
		Pool:         d.Pool,
		Render:       d.Renderer,
		Logger:       d.Logger,
	}
	ipHeader := d.Config.ClientIPHeader
	clientIP := func(r *http.Request) netip.Addr { return middleware.ClientIP(r, trusted, ipHeader) }
	router, err := route.Build(route.Config{
		Resolver:    resolver,
		ClientIP:    clientIP,
		NotFound:    h.NotFound,
		ServerError: h.ServerError,
		Logger:      d.Logger,
	}, Routes(h, d.Assets))
	if err != nil {
		return nil, err
	}
	return &App{Handler: router, Router: router, Resolver: resolver, Links: links, Handlers: h, ClientIP: clientIP}, nil
}
