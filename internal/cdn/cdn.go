// Package cdn is the interface to the CDN's API (BunnyCDN in production):
// purging cached URLs and managing a group's custom hostname on the pull
// zone. See development-plan.md, "CDN Client".
//
// Phase 0 has only the interface and the recording fake in
// internal/testkit/fakes. The Bunny client (internal/cdn/bunny.go) is
// written against Bunny's API reference when it is first needed; its
// behaviour (Host header, cache key, purges on a custom hostname) is only
// proved by the Phase 9 spike against the live service.
package cdn

import "context"

// CDN purges cached pages and manages custom hostnames on the pull zone.
type CDN interface {
	// PurgeURL purges one absolute URL, such as
	// https://hikers.example.com/groups/hikers. Handlers don't build these
	// URLs themselves; they go through the purge helper, which purges a
	// path on every hostname the group is served from.
	PurgeURL(ctx context.Context, absoluteURL string) error

	// AddHostname registers a custom domain on the pull zone.
	AddHostname(ctx context.Context, host string) error

	// LoadFreeCertificate requests the CDN's free TLS certificate for a
	// hostname. It only succeeds once DNS points at the pull zone.
	LoadFreeCertificate(ctx context.Context, host string) error

	// RemoveHostname removes a custom domain from the pull zone.
	RemoveHostname(ctx context.Context, host string) error
}
