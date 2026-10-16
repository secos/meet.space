// Package config loads the server's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Environments the server knows.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// HostSourceHost means the visitor's hostname is the request's Host header.
// Any other HOST_SOURCE value names the header that carries it (decided by
// the Phase 9 Bunny spike).
const HostSourceHost = "host"

// Config is every setting the server reads. Platform identity (name,
// hosts, sender addresses) lives here and nowhere in code, so a rename is
// a config edit (decisions.md question 6).
type Config struct {
	Env        string `env:"ENV" envDefault:"development"`
	ListenAddr string `env:"LISTEN_ADDR" envDefault:":8080"`

	DatabaseURL string `env:"DATABASE_URL"`

	PlatformName  string `env:"PLATFORM_NAME"`
	PublicHost    string `env:"PUBLIC_HOST"`
	PublicBaseURL string `env:"PUBLIC_BASE_URL"`
	OriginHost    string `env:"ORIGIN_HOST"`
	HostSource    string `env:"HOST_SOURCE" envDefault:"host"`
	AppSecret     string `env:"APP_SECRET"`

	// Security Rule 8: the forwarded-for header is trusted only when the
	// request's immediate peer is in TrustedProxies.
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`
	ClientIPHeader string   `env:"CLIENT_IP_HEADER" envDefault:"X-Forwarded-For"`

	SESRegion        string `env:"SES_REGION"`
	SESConfigSetAuth string `env:"SES_CONFIG_SET_AUTH"`
	SESConfigSetNotify string `env:"SES_CONFIG_SET_NOTIFY"`
	SESFromAuth      string `env:"SES_FROM_AUTH"`
	SESFromNotify    string `env:"SES_FROM_NOTIFY"`
	SESReplyTo       string `env:"SES_REPLY_TO"`
	SNSTopicARN      string `env:"SNS_TOPIC_ARN"`

	BunnyAPIKey     string `env:"BUNNY_API_KEY"`
	BunnyPullZoneID string `env:"BUNNY_PULL_ZONE_ID"`

	S3Region string `env:"S3_REGION"`
	S3Bucket string `env:"S3_BUCKET"`

	StripeSecretKey     string `env:"STRIPE_SECRET_KEY"`
	StripeWebhookSecret string `env:"STRIPE_WEBHOOK_SECRET"`
	StripePriceID       string `env:"STRIPE_PRICE_ID"`

	GoogleMapsBrowserKey string `env:"GOOGLE_MAPS_BROWSER_KEY"`
	GoogleMapsEmbedKey   string `env:"GOOGLE_MAPS_EMBED_KEY"`

	// TemplateDir and StaticDir are where templates and assets are read
	// from (bind-mounted in development).
	TemplateDir string `env:"TEMPLATE_DIR" envDefault:"templates"`
	StaticDir   string `env:"STATIC_DIR" envDefault:"static"`
}

// MinSecretLen is the shortest APP_SECRET accepted, in bytes.
const MinSecretLen = 32

// FromEnviron reads the process environment. Only cmd/server calls it;
// tests build a Config with Parse so a developer's real keys never reach
// a test server.
func FromEnviron() (Config, error) {
	return Parse(env.ToMap(os.Environ()))
}

// Parse builds a Config from the given variables and validates it.
func Parse(vars map[string]string) (Config, error) {
	var c Config
	if err := env.ParseWithOptions(&c, env.Options{Environment: vars}); err != nil {
		return Config{}, err
	}
	c.PublicHost = strings.ToLower(c.PublicHost)
	c.OriginHost = strings.ToLower(c.OriginHost)
	return c, c.Validate()
}

// Validate reports every missing or malformed setting at once.
func (c Config) Validate() error {
	var errs []error
	req := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	req("DATABASE_URL", c.DatabaseURL)
	req("PLATFORM_NAME", c.PlatformName)
	req("PUBLIC_HOST", c.PublicHost)
	req("PUBLIC_BASE_URL", c.PublicBaseURL)
	req("ORIGIN_HOST", c.OriginHost)
	req("APP_SECRET", c.AppSecret)
	req("SES_FROM_AUTH", c.SESFromAuth)
	req("SES_FROM_NOTIFY", c.SESFromNotify)

	switch c.Env {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("ENV must be development, test or production, got %q", c.Env))
	}
	if c.AppSecret != "" && len(c.AppSecret) < MinSecretLen {
		errs = append(errs, fmt.Errorf("APP_SECRET must be at least %d bytes", MinSecretLen))
	}
	if c.PublicHost != "" && c.PublicHost == c.OriginHost {
		errs = append(errs, errors.New("PUBLIC_HOST and ORIGIN_HOST must differ"))
	}
	if c.PublicBaseURL != "" {
		u, err := url.Parse(c.PublicBaseURL)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("PUBLIC_BASE_URL: %w", err))
		case u.Scheme != "http" && u.Scheme != "https":
			errs = append(errs, errors.New("PUBLIC_BASE_URL must use http or https"))
		case strings.ToLower(u.Host) != c.PublicHost:
			errs = append(errs, fmt.Errorf("PUBLIC_BASE_URL host %q must equal PUBLIC_HOST %q", u.Host, c.PublicHost))
		case u.Path != "" && u.Path != "/", u.RawQuery != "", u.Fragment != "":
			errs = append(errs, errors.New("PUBLIC_BASE_URL must have no path, query or fragment"))
		}
	}
	if c.HostSource != HostSourceHost && !validHeaderName(c.HostSource) {
		errs = append(errs, fmt.Errorf("HOST_SOURCE must be %q or a header name, got %q", HostSourceHost, c.HostSource))
	}
	if !validHeaderName(c.ClientIPHeader) {
		errs = append(errs, fmt.Errorf("CLIENT_IP_HEADER must be a header name, got %q", c.ClientIPHeader))
	}
	if _, err := c.TrustedProxyPrefixes(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Scheme is the scheme of PUBLIC_BASE_URL ("https" in production, "http"
// in local development). Links to custom domains use the same scheme.
func (c Config) Scheme() string {
	if u, err := url.Parse(c.PublicBaseURL); err == nil && u.Scheme != "" {
		return u.Scheme
	}
	return "https"
}

// TrustedProxyPrefixes parses TRUSTED_PROXIES. A bare address means that
// single address.
func (c Config) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range c.TrustedProxies {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q is not an address or CIDR", s)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// validHeaderName reports whether s is an HTTP field name (RFC 9110 token).
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r > 0x7e || r <= 0x20 || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, r) {
			return false
		}
	}
	return true
}
