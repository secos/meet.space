package main

import (
	"bytes"
	"context"
	"testing"
	"time"

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

func TestUnknownCommandFails(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"migrate", "sideways"}, environ("postgres://x@127.0.0.1:1/x"), &out, &out); err == nil {
		t.Error("unknown command succeeded")
	}
}
