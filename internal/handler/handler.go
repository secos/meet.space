// Package handler holds the HTTP handlers. Every route reaches them through
// the route table (internal/route), which has already resolved the site,
// set the cache policy and run the Origin check.
package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	"meet.space/internal/branding"
	"meet.space/internal/model"
	"meet.space/internal/site"
	"meet.space/internal/tmpl"
)

// Renderer renders pages and fragments (internal/tmpl).
type Renderer interface {
	Render(w http.ResponseWriter, status int, page string, p tmpl.Page) error
	RenderFragment(w http.ResponseWriter, status int, name string, data any) error
}

// Handlers holds what the handlers share.
type Handlers struct {
	PlatformName string
	Pool         *pgxpool.Pool
	Render       Renderer
	Logger       *slog.Logger
}

// SiteFor builds the template data that brands a page for the request's
// site: the platform's name and default color, or the group's name, look
// and (validated) color with no platform marks.
func (h *Handlers) SiteFor(ctx context.Context) (tmpl.Site, error) {
	sc := site.FromContext(ctx)
	switch sc.Kind {
	case site.KindPlatform:
		return tmpl.Site{
			Kind:       "platform",
			SiteName:   h.PlatformName,
			FooterText: h.PlatformName,
			ThemeColor: branding.DefaultBrand,
			Look:       branding.LookClean,
		}, nil
	case site.KindGroup:
		g, err := model.LoadGroupSite(ctx, h.Pool, sc.GroupID)
		if err != nil {
			return tmpl.Site{}, fmt.Errorf("load group %s: %w", sc.GroupID, err)
		}
		color := branding.DefaultBrand
		if branding.ValidHex(g.PrimaryColor) {
			color = g.PrimaryColor
		}
		return tmpl.Site{
			Kind:       "group",
			SiteName:   g.Name,
			FooterText: g.Name,
			ThemeColor: color,
			Look:       branding.LookOrClean(g.Look),
			Initials:   Initials(g.Name),
		}, nil
	}
	return tmpl.Site{}, fmt.Errorf("no page site for %s host", sc.Kind)
}

// Initials returns up to two letters for a name's logo tile: the first
// letters of the first two words, or the first two letters of a single
// word, upper-cased.
func Initials(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []rune
	switch {
	case len(words) >= 2:
		out = append(out, []rune(words[0])[0], []rune(words[1])[0])
	case len(words) == 1:
		rs := []rune(words[0])
		out = rs[:min(2, len(rs))]
	}
	return strings.ToUpper(string(out))
}

// Home is GET /: the platform's landing page, or a custom domain's group
// page. Phase 0 renders placeholders for both.
func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	s, err := h.SiteFor(r.Context())
	if err != nil {
		h.Logger.Error("home: site data", "err", err)
		h.ServerError(w, r)
		return
	}
	page := "home_platform"
	if s.Kind == "group" {
		page = "home_group"
	}
	if err := h.Render.Render(w, http.StatusOK, page, tmpl.Page{Site: s}); err != nil {
		h.Logger.Error("home: render", "err", err)
		h.ServerError(w, r)
	}
}

// Health is GET /health on the origin host: 200 "ok" when the database
// answers within 2 seconds, otherwise 503 (decisions.md "/health").
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := h.Pool.Ping(ctx); err != nil {
		h.Logger.Error("health: database ping failed", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// Error messages (decisions.md "Error pages").
const (
	NotFoundMessage    = "We can't find that page."
	NotFoundLink       = "Go to the home page"
	ServerErrorMessage = "Something went wrong on our side."
	ServerErrorLink    = "Try again"
)

// NotFound writes the site-aware 404 for the platform or a custom domain.
func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, r, tmpl.ErrorContent{
		Status: http.StatusNotFound, Message: NotFoundMessage, LinkText: NotFoundLink, LinkHref: "/",
	})
}

// ServerError writes the site-aware 500.
func (h *Handlers) ServerError(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, r, tmpl.ErrorContent{
		Status: http.StatusInternalServerError, Message: ServerErrorMessage, LinkText: ServerErrorLink, LinkHref: r.URL.RequestURI(),
	})
}

// writeError renders the one-message error page in the site's own layout,
// or just the message for an htmx fragment request. If even that fails it
// falls back to plain text with no name in it.
func (h *Handlers) writeError(w http.ResponseWriter, r *http.Request, e tmpl.ErrorContent) {
	if r.Header.Get("HX-Request") == "true" {
		if err := h.Render.RenderFragment(w, e.Status, "error", e); err == nil {
			return
		}
	} else if s, err := h.SiteFor(r.Context()); err == nil {
		if err := h.Render.Render(w, e.Status, "error", tmpl.Page{Site: s, Title: e.Message, Content: e}); err == nil {
			return
		} else {
			h.Logger.Error("error page: render", "err", err)
		}
	} else {
		h.Logger.Error("error page: site data", "err", err)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(e.Status)
	_, _ = w.Write([]byte(e.Message + "\n"))
}
