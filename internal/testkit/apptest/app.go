// Package apptest builds the whole app for HTTP tests: a fresh database, the
// fake clock, recording fakes, and a client that sends as any host and any
// role. Only test code imports it.
package apptest

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"meet.space/internal/app"
	"meet.space/internal/assets"
	"meet.space/internal/clock/clocktest"
	"meet.space/internal/config"
	"meet.space/internal/site"
	"meet.space/internal/testkit"
	"meet.space/internal/testkit/fakes"
	"meet.space/internal/tmpl"
)

// Hosts the request helper can send as. The app under test is configured
// with these, mirroring the docker compose setup.
const (
	PlatformHost = "localhost:8080"
	OriginHost   = "origin.localhost:8080"
	// GroupHost is the seeded group's active custom domain.
	GroupHost = "hikers.localhost:8080"
	// PendingHost is a group's custom domain still waiting for DNS.
	PendingHost = "pending.localhost:8080"
	// UnknownHost belongs to no site.
	UnknownHost = "unknown.localhost:8080"

	PlatformName = "meet.space"
	// TestSecret is APP_SECRET in tests.
	TestSecret = "test-secret-test-secret-test-secret-0123"
)

// Epoch is the fake clock's starting instant: a Saturday evening in
// Chicago, a week before the 2026 daylight saving change.
var Epoch = time.Date(2026, 10, 24, 19, 0, 0, 0, time.UTC)

// TestEnv is the configuration tests run with, built explicitly so no real
// key from the developer's environment can reach a test server.
func TestEnv(databaseURL string) map[string]string {
	return map[string]string{
		"ENV":             config.EnvTest,
		"DATABASE_URL":    databaseURL,
		"PLATFORM_NAME":   PlatformName,
		"PUBLIC_HOST":     PlatformHost,
		"PUBLIC_BASE_URL": "http://" + PlatformHost,
		"ORIGIN_HOST":     OriginHost,
		"HOST_SOURCE":     "host",
		"APP_SECRET":      TestSecret,
		"SES_FROM_AUTH":   "login@auth.example.test",
		"SES_FROM_NOTIFY": "notifications@mail.example.test",
	}
}

// App is a running app wired to a fresh database, a fake clock and
// recording fakes for every outside service.
type App struct {
	T        testing.TB
	DB       *testkit.DB
	Config   config.Config
	Clock    *clocktest.Fake
	Domains  *site.Domains
	App      *app.App
	Sender   *fakes.Sender
	Storage  *fakes.Storage
	CDN      *fakes.CDN
	Payments *fakes.Payments
	DNS      *fakes.DNS
	logs     *syncBuffer
}

// Option changes the test configuration before the app is built.
type Option func(env map[string]string)

// WithEnv sets one configuration variable.
func WithEnv(key, value string) Option {
	return func(env map[string]string) { env[key] = value }
}

// NewApp builds the app exactly as cmd/server does, with the test database,
// the fake clock and fakes in place of every outside service.
func NewApp(t testing.TB, opts ...Option) *App {
	t.Helper()
	db := testkit.NewDB(t)
	env := TestEnv(db.URL)
	for _, o := range opts {
		o(env)
	}
	cfg, err := config.Parse(env)
	if err != nil {
		t.Fatalf("test config: %v", err)
	}
	root := RepoRoot(t)
	a := &App{
		T:        t,
		DB:       db,
		Config:   cfg,
		Clock:    clocktest.New(Epoch),
		Domains:  site.NewDomains(cfg.PublicHost, cfg.OriginHost),
		Sender:   &fakes.Sender{},
		Storage:  &fakes.Storage{},
		CDN:      &fakes.CDN{},
		Payments: &fakes.Payments{},
		DNS:      &fakes.DNS{},
		logs:     &syncBuffer{},
	}
	assetSet, err := assets.New(filepath.Join(root, "static"), false)
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	renderer, err := tmpl.New(filepath.Join(root, "templates"), assetSet, false)
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	built, err := app.New(app.Deps{
		Config:   cfg,
		Pool:     db.Pool,
		Clock:    a.Clock,
		Domains:  a.Domains,
		Renderer: renderer,
		Assets:   assetSet,
		Logger:   slog.New(slog.NewTextHandler(a.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	a.App = built
	return a
}

// RefreshDomains reloads the custom-domain map from the database.
func (a *App) RefreshDomains() {
	a.T.Helper()
	if err := a.Domains.Load(context.Background(), a.DB.Pool); err != nil {
		a.T.Fatalf("refresh domains: %v", err)
	}
}

// Logs returns everything the app logged so far.
func (a *App) Logs() string { return a.logs.String() }

// RepoRoot finds the repository root (the directory holding go.mod).
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's directory")
		}
		dir = parent
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
