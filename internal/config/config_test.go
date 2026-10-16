package config_test

import (
	"reflect"
	"strings"
	"testing"

	"meet.space/internal/config"
	_ "meet.space/internal/testkit/nonet"
)

func base() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://u@127.0.0.1/db", "PLATFORM_NAME": "meet.space",
		"PUBLIC_HOST": "localhost:8080", "PUBLIC_BASE_URL": "http://localhost:8080",
		"ORIGIN_HOST": "origin.localhost:8080", "APP_SECRET": strings.Repeat("s", 32),
		"SES_FROM_AUTH": "login@auth.example.test", "SES_FROM_NOTIFY": "notifications@mail.example.test",
	}
}

// P0-CONFIG-01: every documented variable is read from the given
// environment, and nothing else is consulted.
func TestParse_ReadsEveryDocumentedVariable(t *testing.T) {
	vars := base()
	vals := map[string]string{
		"ENV": "production", "HOST_SOURCE": "X-Visitor-Host", "SES_REGION": "eu-west-1",
		"SES_CONFIG_SET_AUTH": "auth-set", "SES_CONFIG_SET_NOTIFY": "notify-set", "SES_REPLY_TO": "reply@example.test",
		"SNS_TOPIC_ARN": "arn:aws:sns:x", "BUNNY_API_KEY": "bunny", "BUNNY_PULL_ZONE_ID": "42", "S3_REGION": "eu-west-1",
		"S3_BUCKET": "uploads", "STRIPE_SECRET_KEY": "sk", "STRIPE_WEBHOOK_SECRET": "whsec", "STRIPE_PRICE_ID": "price",
		"GOOGLE_MAPS_BROWSER_KEY": "browser", "GOOGLE_MAPS_EMBED_KEY": "embed", "TRUSTED_PROXIES": "10.0.0.0/8,172.18.0.2",
		"CLIENT_IP_HEADER": "X-Real-Ip",
	}
	for k, v := range vals {
		vars[k] = v
	}
	c, err := config.Parse(vars)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rt, rv := reflect.TypeOf(c), reflect.ValueOf(c)
	for i := range rt.NumField() {
		tag := strings.Split(rt.Field(i).Tag.Get("env"), ",")[0]
		if f := rv.Field(i); f.Kind() == reflect.String {
			got[tag] = f.String()
		}
	}
	for k, v := range vals {
		if k == "TRUSTED_PROXIES" {
			continue
		}
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(c.TrustedProxies) != 2 {
		t.Errorf("TRUSTED_PROXIES: %v", c.TrustedProxies)
	}
	if c.PlatformName != "meet.space" || c.PublicHost != "localhost:8080" || c.OriginHost != "origin.localhost:8080" {
		t.Errorf("identity: %+v", c)
	}
}

// P0-CONFIG-09: Parse reads only the map it is given, never the process
// environment.
func TestParse_IgnoresTheProcessEnvironment(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_from_the_developer")
	t.Setenv("SES_REGION", "us-east-1")
	c, err := config.Parse(base())
	if err != nil {
		t.Fatal(err)
	}
	if c.StripeSecretKey != "" || c.SESRegion != "" {
		t.Errorf("process environment leaked into config: %q %q", c.StripeSecretKey, c.SESRegion)
	}
}

// Hosts in config are lowercased (decisions.md "Host matching"), and the
// link scheme follows PUBLIC_BASE_URL.
func TestParse_LowercasesHostsAndTakesSchemeFromBaseURL(t *testing.T) {
	vars := base()
	vars["PUBLIC_HOST"], vars["PUBLIC_BASE_URL"], vars["ORIGIN_HOST"] = "Meet.Example", "https://Meet.Example", "Origin.Meet.Example"
	c, err := config.Parse(vars)
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicHost != "meet.example" || c.OriginHost != "origin.meet.example" || c.Scheme() != "https" {
		t.Errorf("%q %q %q", c.PublicHost, c.OriginHost, c.Scheme())
	}
}
