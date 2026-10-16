// Package site decides which site a request belongs to, from the visitor's
// hostname: the platform, one group's active custom domain, the origin
// host (health and webhooks only), or nothing (404). It also builds the
// base URLs that links in emails use (Security Rule 2).
package site

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Kind is the kind of site a host serves.
type Kind uint8

const (
	// KindNone is an unknown host: every request gets the bare 404.
	KindNone Kind = iota
	// KindPlatform is PUBLIC_HOST: the full platform.
	KindPlatform
	// KindGroup is an active custom domain: one group, white-labeled.
	KindGroup
	// KindOrigin is ORIGIN_HOST: /health and /webhooks/* only.
	KindOrigin
)

func (k Kind) String() string {
	switch k {
	case KindPlatform:
		return "platform"
	case KindGroup:
		return "group"
	case KindOrigin:
		return "origin"
	default:
		return "none"
	}
}

// Context is the resolved site of one request.
type Context struct {
	Kind Kind
	// Host is the canonical visitor host (lowercased, port included). It is
	// the only host the Origin check, session binding and current-site
	// links may use. Empty for KindNone.
	Host string
	// GroupID is the group a custom domain serves (KindGroup only).
	GroupID uuid.UUID
}

type ctxKey struct{}

// NewContext returns ctx carrying c.
func NewContext(ctx context.Context, c Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// FromContext returns the site resolved for this request, or a KindNone
// Context if none was resolved.
func FromContext(ctx context.Context) Context {
	c, _ := ctx.Value(ctxKey{}).(Context)
	return c
}

// Domains is the in-memory map of active custom domains, loaded at startup
// and refreshed whenever a domain changes state. Lookups never query the
// database, and a refresh swaps the whole map at once.
type Domains struct {
	m atomic.Pointer[domainMaps]
	// reserved hosts (the platform and origin hosts) never enter the map,
	// so no groups row can capture them or have links built for them.
	reserved map[string]bool
}

type domainMaps struct {
	byHost  map[string]uuid.UUID
	byGroup map[uuid.UUID]string
}

// Querier is the part of a pool or transaction Load needs.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// NewDomains returns an empty map. The platform and origin hosts are never
// added to it, whatever the database holds.
func NewDomains(publicHost, originHost string) *Domains {
	d := &Domains{reserved: map[string]bool{
		strings.ToLower(publicHost): true,
		strings.ToLower(originHost): true,
	}}
	d.m.Store(&domainMaps{byHost: map[string]uuid.UUID{}, byGroup: map[uuid.UUID]string{}})
	return d
}

// Load replaces the map with every group whose custom domain is active.
// On error the previous map stays in place.
func (d *Domains) Load(ctx context.Context, q Querier) error {
	rows, err := q.Query(ctx, `SELECT custom_domain, id FROM groups
		WHERE custom_domain IS NOT NULL AND custom_domain_status = 'active'`)
	if err != nil {
		return err
	}
	next := &domainMaps{byHost: map[string]uuid.UUID{}, byGroup: map[uuid.UUID]string{}}
	for rows.Next() {
		var host string
		var id uuid.UUID
		if err := rows.Scan(&host, &id); err != nil {
			rows.Close()
			return err
		}
		host = strings.ToLower(host)
		if d.reserved[host] {
			continue
		}
		next.byHost[host] = id
		next.byGroup[id] = host
	}
	if err := rows.Err(); err != nil {
		return err
	}
	d.m.Store(next)
	return nil
}

// Set replaces the map with hosts (host → group). Hosts are lowercased.
func (d *Domains) Set(hosts map[string]uuid.UUID) {
	next := &domainMaps{byHost: map[string]uuid.UUID{}, byGroup: map[uuid.UUID]string{}}
	for h, id := range hosts {
		h = strings.ToLower(h)
		if d.reserved[h] {
			continue
		}
		next.byHost[h] = id
		next.byGroup[id] = h
	}
	d.m.Store(next)
}

// Lookup returns the group an active custom domain serves.
func (d *Domains) Lookup(host string) (uuid.UUID, bool) {
	id, ok := d.m.Load().byHost[host]
	return id, ok
}

// ActiveHost returns the group's active custom domain, if it has one.
func (d *Domains) ActiveHost(groupID uuid.UUID) (string, bool) {
	h, ok := d.m.Load().byGroup[groupID]
	return h, ok
}

// Resolver turns a request into a site Context.
type Resolver struct {
	PublicHost string
	OriginHost string
	// HostSource is "host" (read the Host header) or the name of the header
	// that carries the visitor's hostname (set from the Phase 9 spike).
	HostSource string
	Domains    *Domains
}

// VisitorHost returns the visitor's hostname, lowercased (decisions.md
// "Host matching" and "HOST_SOURCE header edge cases"). ok is false when
// the configured header is repeated or holds several values.
func (r *Resolver) VisitorHost(req *http.Request) (host string, ok bool) {
	if r.HostSource != "" && !strings.EqualFold(r.HostSource, "host") {
		vals := req.Header.Values(r.HostSource)
		switch {
		case len(vals) > 1:
			return "", false
		case len(vals) == 1:
			v := strings.TrimSpace(vals[0])
			// An empty value or several values can't be trusted; don't guess.
			if v == "" || strings.Contains(v, ",") {
				return "", false
			}
			return strings.ToLower(v), true
		}
	}
	return strings.ToLower(req.Host), true
}

// Resolve maps the request's visitor host to a site. PUBLIC_HOST and
// ORIGIN_HOST are matched before custom domains, so a groups row naming
// either can never capture it. Matching is exact, port included.
func (r *Resolver) Resolve(req *http.Request) Context {
	host, ok := r.VisitorHost(req)
	if !ok || host == "" {
		return Context{}
	}
	switch host {
	case r.PublicHost:
		return Context{Kind: KindPlatform, Host: host}
	case r.OriginHost:
		return Context{Kind: KindOrigin, Host: host}
	}
	if id, ok := r.Domains.Lookup(host); ok {
		return Context{Kind: KindGroup, Host: host, GroupID: id}
	}
	return Context{}
}

// Links builds absolute URLs. Links in emails come only from here, never
// from a request's Host header (Security Rule 2).
type Links struct {
	// Scheme is PUBLIC_BASE_URL's scheme; custom domains use the same one,
	// so local development links use http.
	Scheme        string
	PublicBaseURL string
	PublicHost    string
	Domains       *Domains
}

// ErrNoBaseURL means the site has no public base URL (the origin host or an
// unknown host).
var ErrNoBaseURL = errors.New("site: no base URL for this host")

// BaseURL returns the base URL for links about a group: its custom domain
// while the site cache holds it as active, otherwise PUBLIC_BASE_URL.
func (l Links) BaseURL(groupID uuid.UUID) string {
	if host, ok := l.Domains.ActiveHost(groupID); ok {
		return l.Scheme + "://" + host
	}
	return strings.TrimSuffix(l.PublicBaseURL, "/")
}

// CurrentBaseURL returns the base URL of the site a request is on, for
// links that must return to the same site (magic links). Only the platform
// and an active custom domain have one.
func (l Links) CurrentBaseURL(c Context) (string, error) {
	switch c.Kind {
	case KindPlatform:
		return strings.TrimSuffix(l.PublicBaseURL, "/"), nil
	case KindGroup:
		if host, ok := l.Domains.ActiveHost(c.GroupID); ok && host == c.Host {
			return l.Scheme + "://" + host, nil
		}
	}
	return "", ErrNoBaseURL
}

// GroupHosts lists every host a group's pages are served from: the
// platform host, plus its custom domain while active. CDN purges cover
// all of them.
func (l Links) GroupHosts(groupID uuid.UUID) []string {
	hosts := []string{l.PublicHost}
	if host, ok := l.Domains.ActiveHost(groupID); ok {
		hosts = append(hosts, host)
	}
	return hosts
}
