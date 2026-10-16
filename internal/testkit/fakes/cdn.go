package fakes

import "context"

// CDNCall is one call to CDN.
type CDNCall struct {
	Meta
	// URL is the absolute URL passed to PurgeURL.
	URL string
	// Host is the hostname passed to AddHostname, LoadFreeCertificate or
	// RemoveHostname.
	Host string
}

func (c *CDNCall) clone() CDNCall { return *c }

// CDN is a recording cdn.CDN. It keeps no state beyond the call log: it
// does not model which hostnames are on the pull zone.
type CDN struct {
	recorder[CDNCall, *CDNCall]
}

// PurgeURL records a purge of absoluteURL.
func (f *CDN) PurgeURL(ctx context.Context, absoluteURL string) error {
	return f.do(ctx, "PurgeURL", CDNCall{URL: absoluteURL}, nil).Err
}

// AddHostname records adding host to the pull zone.
func (f *CDN) AddHostname(ctx context.Context, host string) error {
	return f.do(ctx, "AddHostname", CDNCall{Host: host}, nil).Err
}

// LoadFreeCertificate records a certificate request for host.
func (f *CDN) LoadFreeCertificate(ctx context.Context, host string) error {
	return f.do(ctx, "LoadFreeCertificate", CDNCall{Host: host}, nil).Err
}

// RemoveHostname records removing host from the pull zone.
func (f *CDN) RemoveHostname(ctx context.Context, host string) error {
	return f.do(ctx, "RemoveHostname", CDNCall{Host: host}, nil).Err
}

// Purged returns the URLs of every successful PurgeURL call, in call
// order.
func (f *CDN) Purged() []string {
	var out []string
	for _, c := range f.Calls() {
		if c.Method == "PurgeURL" && c.Done && c.Err == nil {
			out = append(out, c.URL)
		}
	}
	return out
}
