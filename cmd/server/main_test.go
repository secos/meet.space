package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"meet.space/internal/clock/clocktest"
	"meet.space/internal/email"
	"meet.space/internal/migrate"
	"meet.space/internal/testkit"
	"meet.space/internal/testkit/apptest"
)

func environ(databaseURL string, extra ...string) []string {
	var out []string
	for k, v := range apptest.TestEnv(databaseURL) {
		out = append(out, k+"="+v)
	}
	return append(out, extra...)
}

func appTables(t *testing.T, url string) int {
	t.Helper()
	db := testkit.Open(t, url)
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// P0-TOOLING-05: `server migrate up` applies migration 001 and exits;
// `server migrate down` reverts it.
func TestMigrateCommand_UpThenDown(t *testing.T) {
	_, url := testkit.NewEmptyDB(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"migrate", "up"}, environ(url), &out, &out); err != nil {
		t.Fatalf("migrate up: %v\n%s", err, out.String())
	}
	if v, dirty, err := migrate.Version(url); err != nil || v != 1 || dirty {
		t.Fatalf("after up: version %d dirty %v err %v", v, dirty, err)
	}
	if n := appTables(t, url); n != 16 {
		t.Errorf("after up: %d tables, want 16", n)
	}
	if err := run(context.Background(), []string{"migrate", "down"}, environ(url), &out, &out); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if n := appTables(t, url); n != 0 {
		t.Errorf("after down: %d tables, want 0", n)
	}
}

// P0-TOOLING-06: starting the server never runs migrations. (What it does
// when the schema is missing is open question 19; this only checks that
// nothing was migrated.)
func TestServe_NeverRunsMigrations(t *testing.T) {
	_, url := testkit.NewEmptyDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	_ = run(ctx, nil, environ(url, "LISTEN_ADDR=127.0.0.1:0"), &out, &out)
	if v, _, err := migrate.Version(url); err != nil || v != 0 {
		t.Errorf("serving migrated the database: version %d, err %v", v, err)
	}
	if n := appTables(t, url); n != 0 {
		t.Errorf("serving created %d tables", n)
	}
}

// P0-HARNESS-19: the dev seed creates the hikers group on its active custom
// domain, is idempotent, and refuses to run in production.
func TestSeedDev(t *testing.T) {
	db := testkit.NewDB(t)
	var out bytes.Buffer
	for range 2 {
		if err := run(context.Background(), []string{"seed-dev"}, environ(db.URL), &out, &out); err != nil {
			t.Fatalf("seed-dev: %v\n%s", err, out.String())
		}
	}
	var domain, status, tier string
	var limit *int
	var storage int64
	var groups, owners int
	ctx := context.Background()
	if err := db.Pool.QueryRow(ctx, `SELECT custom_domain, custom_domain_status, tier, member_limit, storage_limit_bytes
		FROM groups WHERE slug = 'hikers'`).Scan(&domain, &status, &tier, &limit, &storage); err != nil {
		t.Fatal(err)
	}
	if domain != "hikers."+apptest.PlatformHost || status != "active" || tier != "paid" || limit != nil || storage != 25<<30 {
		t.Errorf("seeded group: %s %s %s %v %d", domain, status, tier, limit, storage)
	}
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM groups`).Scan(&groups)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE role = 'owner'`).Scan(&owners)
	if groups != 1 || owners != 1 {
		t.Errorf("after two runs: %d groups, %d owners", groups, owners)
	}
	if err := run(context.Background(), []string{"seed-dev"}, environ(db.URL, "ENV=production"), &out, &out); err == nil {
		t.Error("seed-dev ran with ENV=production")
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// Plan Phase 0 step 10: serving runs the outbox poller, which sends queued
// email through the dev sender (SES_REGION empty) to the server's output
// with the configured From address, and stops with the server.
func TestServe_RunsTheOutbox(t *testing.T) {
	db := testkit.NewDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, environ(db.URL, "LISTEN_ADDR=127.0.0.1:0", "STATIC_DIR=../../static", "TEMPLATE_DIR=../../templates"), out, out)
	}()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := email.Enqueue(ctx, tx, clocktest.New(time.Now().Add(-time.Minute)), email.Outgoing{
		Stream: email.StreamAuth, Kind: "magic_link", To: "ann@example.com", FromName: "Hikers",
		Subject: "Your login link", HTML: "<p>x</p>", Text: "verify/abc123", DedupeKey: "serve-test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err := db.Pool.QueryRow(ctx, `SELECT status FROM email_outbox WHERE dedupe_key = 'serve-test'`).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "sent" {
			break
		}
	}
	if status != "sent" {
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\n%s", err, out.String())
		default:
		}
		t.Fatalf("queued email is %s after 10s of serving\n%s", status, out.String())
	}
	if s := out.String(); !strings.Contains(s, "From: Hikers <login@auth.example.test>") || !strings.Contains(s, "verify/abc123") {
		t.Errorf("dev sender output lacks the email:\n%s", s)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"migrate", "sideways"}, environ("postgres://x@127.0.0.1:1/x"), &out, &out); err == nil {
		t.Error("unknown command succeeded")
	}
}
