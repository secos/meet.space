// Package testkit is the test harness: a real Postgres database per test,
// recording fakes for every outside service, a fake clock, and a request
// helper that speaks to the in-process app as any host and any role.
// Only test code imports it.
package testkit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"meet.space/internal/database"
	"meet.space/internal/migrate"
	"meet.space/migrations"
)

// DefaultAdminURL reaches the docker compose Postgres. Override it with
// TEST_DATABASE_URL. Its database is only used to create and drop test
// databases; the dev database is never touched.
const DefaultAdminURL = "postgres://meetspace:devpassword@127.0.0.1:5432/postgres?sslmode=disable"

const (
	testDBPrefix   = "meetspace_t_"
	templatePrefix = "meetspace_tmpl_"
	// staleAfter is how old a leftover test database (from a crashed run)
	// must be before it is dropped.
	staleAfter = 2 * time.Hour
	lockKey    = 7_314_202_610 // advisory lock serializing template work
)

// DB is one test's private database.
type DB struct {
	Name string
	URL  string
	Pool *pgxpool.Pool
}

var (
	tmplOnce sync.Once
	tmplName string
	tmplErr  error
)

// AdminURL is the maintenance connection used to create test databases.
func AdminURL() string {
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	return DefaultAdminURL
}

// NewDB gives the test a fresh database cloned from a template that was
// migrated with the real migration files through the same code path as
// `server migrate up`. It is dropped when the test ends. If Postgres is
// unreachable the test fails; it is never skipped.
func NewDB(t testing.TB) *DB {
	t.Helper()
	tmplOnce.Do(func() { tmplName, tmplErr = ensureTemplate() })
	if tmplErr != nil {
		t.Fatalf("test database template: %v (is `docker compose up -d db` running?)", tmplErr)
	}
	ctx := context.Background()
	name := testDBPrefix + strconv.FormatInt(time.Now().Unix(), 10) + "_" + randHex(6)
	admin := adminConn(t)
	defer admin.Close(ctx)
	if err := withLock(ctx, admin, func() error {
		_, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{tmplName}.Sanitize())
		return err
	}); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	u := dbURL(t, name)
	pool, err := database.Open(ctx, u)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), AdminURL())
		if err != nil {
			t.Errorf("drop test database %s: %v", name, err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})
	return &DB{Name: name, URL: u, Pool: pool}
}

// NewEmptyDB gives the test a fresh database with no schema at all, for
// migration tests. It is dropped when the test ends.
func NewEmptyDB(t testing.TB) (name, url string) {
	t.Helper()
	ctx := context.Background()
	name = testDBPrefix + strconv.FormatInt(time.Now().Unix(), 10) + "_" + randHex(6)
	admin := adminConn(t)
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create empty database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), AdminURL())
		if err != nil {
			t.Errorf("drop database %s: %v", name, err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})
	return name, dbURL(t, name)
}

func adminConn(t testing.TB) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), AdminURL())
	if err != nil {
		t.Fatalf("connect to test Postgres at %s: %v (is `docker compose up -d db` running?)", redact(AdminURL()), err)
	}
	return c
}

func dbURL(t testing.TB, name string) string {
	t.Helper()
	u, err := url.Parse(AdminURL())
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// ensureTemplate builds (once per migration content) a database migrated
// up, then down, then up again, so every run also proves the down
// migration and the round trip. Concurrent test processes serialize on an
// advisory lock.
func ensureTemplate() (string, error) {
	sum, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := templatePrefix + sum
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, AdminURL())
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", redact(AdminURL()), err)
	}
	defer admin.Close(ctx)
	err = withLock(ctx, admin, func() error {
		dropStale(ctx, admin, name)
		var exists bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		building := name + "_building"
		ident := pgx.Identifier{building}.Sanitize()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			return err
		}
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
			return err
		}
		u, err := url.Parse(AdminURL())
		if err != nil {
			return err
		}
		u.Path = "/" + building
		for _, step := range []func(string) error{migrate.Up, downAll, migrate.Up} {
			if err := step(u.String()); err != nil {
				_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)")
				return fmt.Errorf("migrating template: %w", err)
			}
		}
		_, err = admin.Exec(ctx, "ALTER DATABASE "+ident+" RENAME TO "+pgx.Identifier{name}.Sanitize())
		return err
	})
	return name, err
}

// downAll reverts every migration, one step at a time.
func downAll(u string) error {
	for {
		v, _, err := migrate.Version(u)
		if err != nil {
			return err
		}
		if v == 0 {
			return nil
		}
		if err := migrate.Down(u); err != nil {
			return err
		}
	}
}

func withLock(ctx context.Context, c *pgx.Conn, fn func() error) error {
	if _, err := c.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return err
	}
	defer c.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey) //nolint:errcheck
	return fn()
}

var testDBName = regexp.MustCompile(`^` + testDBPrefix + `(\d+)_[0-9a-f]+$`)

// dropStale removes templates for other migration contents and test
// databases left behind by crashed runs.
func dropStale(ctx context.Context, c *pgx.Conn, keep string) {
	rows, err := c.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'meetspace\\_t%'")
	if err != nil {
		return
	}
	names, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	cutoff := time.Now().Add(-staleAfter).Unix()
	for _, n := range names {
		drop := false
		if strings.HasPrefix(n, templatePrefix) && n != keep {
			drop = true
		}
		if m := testDBName.FindStringSubmatch(n); m != nil {
			if ts, err := strconv.ParseInt(m[1], 10, 64); err == nil && ts < cutoff {
				drop = true
			}
		}
		if drop {
			_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{n}.Sanitize()+" WITH (FORCE)")
		}
	}
}

// migrationsHash identifies the migration files' content, so editing a
// migration rebuilds the template rather than testing a stale schema.
func migrationsHash() (string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func redact(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "(unparseable url)"
	}
	return p.Redacted()
}
