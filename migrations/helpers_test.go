package migrations_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"meet.space/internal/database"
	"meet.space/internal/testkit"
)

// appTables are the 16 tables of the plan's migration 001
// (development-plan.md "Database Schema"; prd.md §7 lists the same 16).
var appTables = []string{
	"attachments", "email_events", "email_opt_outs", "email_outbox",
	"email_suppressions", "groups", "magic_links", "meetings", "memberships",
	"notification_preferences", "posts", "rsvps", "sessions", "stripe_events",
	"subscriptions", "users",
}

// idTables are the tables whose primary key is a UUID `id` column, which
// the plan says is generated in Go with uuid.NewV7(). The other three are
// keyed by natural values (email_opt_outs, email_suppressions,
// stripe_events).
var idTables = []string{
	"attachments", "email_events", "email_outbox", "groups", "magic_links",
	"meetings", "memberships", "notification_preferences", "posts", "rsvps",
	"sessions", "subscriptions", "users",
}

// primaryKey names the columns that identify a row of each table.
func primaryKey(table string) []string {
	switch table {
	case "email_opt_outs":
		return []string{"email", "group_id"}
	case "email_suppressions":
		return []string{"email"}
	case "stripe_events":
		return []string{"event_id"}
	default:
		return []string{"id"}
	}
}

// t0 is a fixed instant for rows that need a time; tests never read the
// wall clock.
var t0 = time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)

// row is one row to insert, column → value. A nil value inserts NULL; a
// missing column is left to its default.
type row map[string]any

// with returns a copy with the given column/value pairs set.
func (r row) with(kv ...any) row {
	c := maps.Clone(r)
	if c == nil {
		c = row{}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		c[kv[i].(string)] = kv[i+1]
	}
	return c
}

// without returns a copy with the columns removed, so they take their
// defaults (or NULL).
func (r row) without(cols ...string) row {
	c := maps.Clone(r)
	for _, col := range cols {
		delete(c, col)
	}
	return c
}

func (r row) id() uuid.UUID { return r["id"].(uuid.UUID) }

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type queryer interface {
	execer
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertSQL builds INSERT INTO table (cols...) VALUES ($1...) suffix.
func insertSQL(table string, r row, suffix string) (string, []any) {
	cols := slices.Sorted(maps.Keys(r))
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		names[i] = pgx.Identifier{c}.Sanitize()
		marks[i] = fmt.Sprintf("$%d", i+1)
		args[i] = r[c]
	}
	sql := fmt.Sprintf("INSERT INTO %s AS t (%s) VALUES (%s)", pgx.Identifier{table}.Sanitize(),
		strings.Join(names, ", "), strings.Join(marks, ", "))
	if len(cols) == 0 {
		sql = fmt.Sprintf("INSERT INTO %s AS t DEFAULT VALUES", pgx.Identifier{table}.Sanitize())
	}
	if suffix != "" {
		sql += " " + suffix
	}
	return sql, args
}

func insertRow(ctx context.Context, q execer, table string, r row) error {
	sql, args := insertSQL(table, r, "")
	_, err := q.Exec(ctx, sql, args...)
	return err
}

// fx builds valid rows against one test database. Every row it builds
// satisfies every constraint, creating whatever parent rows it references,
// so a test can change one column and know that column alone decides the
// outcome.
type fx struct {
	t   testing.TB
	ctx context.Context
	q   *pgxpool.Pool
	n   *atomic.Int64 // shared by every copy made with on
}

// newFx gives the test its own migrated database.
func newFx(t testing.TB) *fx {
	t.Helper()
	db := testkit.NewDB(t)
	return newFxOn(t, db.Pool)
}

func newFxOn(t testing.TB, pool *pgxpool.Pool) *fx {
	return &fx{t: t, ctx: context.Background(), q: pool, n: new(atomic.Int64)}
}

// on returns the same fixture reporting to t, for use inside a subtest.
func (f *fx) on(t testing.TB) *fx {
	return &fx{t: t, ctx: f.ctx, q: f.q, n: f.n}
}

func (f *fx) seq() int64 { return f.n.Add(1) }

func (f *fx) uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, f.seq()) }

func (f *fx) email(prefix string) string { return fmt.Sprintf("%s%d@example.com", prefix, f.seq()) }

func newID(t testing.TB) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	return id
}

// row returns a valid row for table with the given overrides applied.
// Parents are created only for references the overrides don't supply.
func (f *fx) row(table string, kv ...any) row {
	f.t.Helper()
	r := row{}.with(kv...)
	set := func(col string, v func() any) {
		if _, ok := r[col]; !ok {
			r[col] = v()
		}
	}
	id := func() any { return newID(f.t) }
	switch table {
	case "users":
		set("id", id)
		set("email", func() any { return f.email("user") })
	case "sessions":
		set("id", id)
		set("user_id", func() any { return f.user() })
		set("host", func() any { return "localhost:8080" })
		set("token_hash", func() any { return f.uniq("session-hash") })
		set("expires_at", func() any { return t0.Add(30 * 24 * time.Hour) })
	case "magic_links":
		set("id", id)
		set("email", func() any { return f.email("link") })
		set("host", func() any { return "localhost:8080" })
		set("token_hash", func() any { return f.uniq("link-hash") })
		set("expires_at", func() any { return t0.Add(15 * time.Minute) })
	case "groups":
		set("id", id)
		set("slug", func() any { return f.uniq("group") })
		set("name", func() any { return "Trail Hikers" })
		set("timezone", func() any { return "America/Chicago" })
	case "memberships":
		set("id", id)
		set("user_id", func() any { return f.user() })
		set("group_id", func() any { return f.group() })
	case "meetings":
		set("id", id)
		set("group_id", func() any { return f.group() })
		set("title", func() any { return "Saturday hike" })
		set("starts_at", func() any { return t0 })
		set("timezone", func() any { return "America/Chicago" })
	case "rsvps":
		set("id", id)
		set("meeting_id", func() any { return f.meeting(f.group()) })
		set("email", func() any { return f.email("rsvp") })
	case "posts":
		set("id", id)
		set("group_id", func() any { return f.group() })
		set("user_id", func() any { return f.user() })
		set("content", func() any { return "See you at the trailhead" })
	case "attachments":
		set("id", id)
		if _, ok := r["post_id"]; !ok {
			g := f.group()
			r["post_id"] = f.post(g)
			set("group_id", func() any { return g })
		}
		set("group_id", func() any { return f.group() })
		set("filename", func() any { return "map.pdf" })
		set("content_type", func() any { return "application/pdf" })
		set("storage_key", func() any { return f.uniq("uploads/key") })
		set("size_bytes", func() any { return int64(1024) })
	case "notification_preferences":
		set("id", id)
		set("user_id", func() any { return f.user() })
		set("group_id", func() any { return f.group() })
	case "email_opt_outs":
		set("email", func() any { return f.email("optout") })
		set("group_id", func() any { return f.group() })
	case "subscriptions":
		set("id", id)
		set("group_id", func() any { return f.group() })
		set("stripe_customer_id", func() any { return f.uniq("cus") })
		set("stripe_subscription_id", func() any { return f.uniq("sub") })
		set("status", func() any { return "active" })
		set("current_period_end", func() any { return t0.Add(365 * 24 * time.Hour) })
	case "stripe_events":
		set("event_id", func() any { return f.uniq("evt") })
	case "email_outbox":
		set("id", id)
		set("stream", func() any { return "auth" })
		set("kind", func() any { return "magic_link" })
		set("to_email", func() any { return f.email("to") })
		set("from_name", func() any { return "Trail Hikers" })
		set("subject", func() any { return "Your login link" })
		set("body_html", func() any { return "<p>Log in</p>" })
		set("body_text", func() any { return "Log in" })
		set("dedupe_key", func() any { return f.uniq("magic_link") })
	case "email_suppressions":
		set("email", func() any { return f.email("suppressed") })
		set("reason", func() any { return "hard_bounce" })
	case "email_events":
		set("id", id)
		set("event_type", func() any { return "delivery" })
	default:
		f.t.Fatalf("fx.row: unknown table %q", table)
	}
	return r
}

// insert tries to insert r, returning the database's error.
func (f *fx) insert(table string, r row) error { return insertRow(f.ctx, f.q, table, r) }

// add inserts a valid row (with overrides) and fails the test if the
// database refuses it.
func (f *fx) add(table string, kv ...any) row {
	f.t.Helper()
	r := f.row(table, kv...)
	if err := f.insert(table, r); err != nil {
		f.t.Fatalf("insert %s %v: %v", table, r, err)
	}
	return r
}

func (f *fx) user(kv ...any) uuid.UUID  { f.t.Helper(); return f.add("users", kv...).id() }
func (f *fx) group(kv ...any) uuid.UUID { f.t.Helper(); return f.add("groups", kv...).id() }

func (f *fx) meeting(group uuid.UUID, kv ...any) uuid.UUID {
	f.t.Helper()
	return f.add("meetings", append([]any{"group_id", group}, kv...)...).id()
}

func (f *fx) post(group uuid.UUID, kv ...any) uuid.UUID {
	f.t.Helper()
	return f.add("posts", append([]any{"group_id", group}, kv...)...).id()
}

func (f *fx) member(user, group uuid.UUID, role string) uuid.UUID {
	f.t.Helper()
	return f.add("memberships", "user_id", user, "group_id", group, "role", role).id()
}

// exec runs a statement that must succeed and returns rows affected.
func (f *fx) exec(sql string, args ...any) int64 {
	f.t.Helper()
	tag, err := f.q.Exec(f.ctx, sql, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return tag.RowsAffected()
}

// try runs a statement and returns its error.
func (f *fx) try(sql string, args ...any) error {
	_, err := f.q.Exec(f.ctx, sql, args...)
	return err
}

// scan runs a one-row query that must succeed.
func (f *fx) scan(sql string, args []any, dest ...any) {
	f.t.Helper()
	if err := f.q.QueryRow(f.ctx, sql, args...).Scan(dest...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fx) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	f.scan(sql, args, &n)
	return n
}

// rowCount is the number of rows in table.
func (f *fx) rowCount(table string) int {
	f.t.Helper()
	return f.count("SELECT count(*) FROM " + pgx.Identifier{table}.Sanitize())
}

// inserted inserts r in its own transaction and returns the stored row as
// JSON (so defaults can be compared) and the transaction's now(), which is
// what a DEFAULT NOW() column must hold.
func (f *fx) inserted(table string, r row) (stored map[string]any, now string) {
	f.t.Helper()
	sql, args := insertSQL(table, r, "RETURNING to_jsonb(t), to_jsonb(now()) #>> '{}'")
	tx, err := f.q.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(f.ctx) //nolint:errcheck
	if err := tx.QueryRow(f.ctx, sql, args...).Scan(&stored, &now); err != nil {
		f.t.Fatalf("insert %s %v: %v", table, r, err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return stored, now
}

// wantStored compares the named columns of a stored row (as JSON values:
// numbers are float64, NULL is nil).
func wantStored(t testing.TB, stored map[string]any, want map[string]any) {
	t.Helper()
	for _, col := range slices.Sorted(maps.Keys(want)) {
		got, ok := stored[col]
		if !ok {
			t.Errorf("column %s missing from the stored row", col)
			continue
		}
		if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want[col]) {
			t.Errorf("%s = %#v, want %#v", col, got, want[col])
		}
	}
}

// ---- catalog dumps -------------------------------------------------------

// catalogEntry is one fact about the schema, attributed to a table so a
// comparison can leave tables out.
type catalogEntry struct {
	table string
	text  string
}

// dumpCatalog describes everything in the public schema that a migration
// can create: relations, every column (type, nullability, default,
// generation, identity, collation, position), every constraint with its
// full definition, every index with its full definition (uniqueness,
// method, columns, predicate), user triggers, functions, standalone types
// and installed extensions. Entries for the tables in skip are left out.
// The result is sorted, so two dumps compare line by line.
func dumpCatalog(t testing.TB, q queryer, skip ...string) []string {
	t.Helper()
	ctx := context.Background()
	var entries []catalogEntry
	collect := func(sql string, scan func(pgx.Rows) (catalogEntry, error)) {
		rows, err := q.Query(ctx, sql)
		if err != nil {
			t.Fatalf("catalog query: %v\n%s", err, sql)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scan(rows)
			if err != nil {
				t.Fatalf("catalog scan: %v", err)
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("catalog rows: %v", err)
		}
	}

	collect(`SELECT c.relname, c.relkind::text FROM pg_class c
	         WHERE c.relnamespace = 'public'::regnamespace AND c.relkind <> 'i'`,
		func(r pgx.Rows) (catalogEntry, error) {
			var name, kind string
			err := r.Scan(&name, &kind)
			return catalogEntry{name, fmt.Sprintf("relation %s kind=%s", name, kind)}, err
		})
	collect(`SELECT c.relname, a.attnum, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
	                coalesce(pg_get_expr(d.adbin, d.adrelid), ''), a.attgenerated::text, a.attidentity::text,
	                coalesce(co.collname, '')
	         FROM pg_attribute a
	         JOIN pg_class c ON c.oid = a.attrelid
	         LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
	         LEFT JOIN pg_collation co ON co.oid = a.attcollation
	         WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
	           AND a.attnum > 0 AND NOT a.attisdropped`,
		func(r pgx.Rows) (catalogEntry, error) {
			var table, col, typ, def, gen, ident, coll string
			var num int
			var notNull bool
			err := r.Scan(&table, &num, &col, &typ, &notNull, &def, &gen, &ident, &coll)
			return catalogEntry{table, fmt.Sprintf("column %s.%s position=%d type=%s notnull=%t default=%q generated=%q identity=%q collation=%q",
				table, col, num, typ, notNull, def, gen, ident, coll)}, err
		})
	collect(`SELECT cl.relname, con.conname, con.contype::text, pg_get_constraintdef(con.oid, true),
	                con.condeferrable, con.condeferred
	         FROM pg_constraint con JOIN pg_class cl ON cl.oid = con.conrelid
	         WHERE con.connamespace = 'public'::regnamespace`,
		func(r pgx.Rows) (catalogEntry, error) {
			var table, name, typ, def string
			var deferrable, deferred bool
			err := r.Scan(&table, &name, &typ, &def, &deferrable, &deferred)
			return catalogEntry{table, fmt.Sprintf("constraint %s.%s type=%s deferrable=%t deferred=%t %s",
				table, name, typ, deferrable, deferred, def)}, err
		})
	collect(`SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname = 'public'`,
		func(r pgx.Rows) (catalogEntry, error) {
			var table, name, def string
			err := r.Scan(&table, &name, &def)
			return catalogEntry{table, fmt.Sprintf("index %s.%s %s", table, name, def)}, err
		})
	collect(`SELECT c.relname, tg.tgname, pg_get_triggerdef(tg.oid)
	         FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid
	         WHERE c.relnamespace = 'public'::regnamespace AND NOT tg.tgisinternal`,
		func(r pgx.Rows) (catalogEntry, error) {
			var table, name, def string
			err := r.Scan(&table, &name, &def)
			return catalogEntry{table, fmt.Sprintf("trigger %s.%s %s", table, name, def)}, err
		})
	collect(`SELECT p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')'
	         FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace`,
		func(r pgx.Rows) (catalogEntry, error) {
			var sig string
			err := r.Scan(&sig)
			return catalogEntry{"", "function " + sig}, err
		})
	collect(`SELECT t.typname, t.typtype::text FROM pg_type t
	         WHERE t.typnamespace = 'public'::regnamespace AND t.typrelid = 0 AND t.typcategory <> 'A'`,
		func(r pgx.Rows) (catalogEntry, error) {
			var name, typ string
			err := r.Scan(&name, &typ)
			return catalogEntry{"", fmt.Sprintf("type %s kind=%s", name, typ)}, err
		})
	collect(`SELECT extname FROM pg_extension`,
		func(r pgx.Rows) (catalogEntry, error) {
			var name string
			err := r.Scan(&name)
			return catalogEntry{"", "extension " + name}, err
		})

	var out []string
	for _, e := range entries {
		if e.table != "" && slices.Contains(skip, e.table) {
			continue
		}
		out = append(out, e.text)
	}
	sort.Strings(out)
	return out
}

// diffLines returns the lines only in a and only in b.
func diffLines(a, b []string) (onlyA, onlyB []string) {
	inB := map[string]bool{}
	for _, l := range b {
		inB[l] = true
	}
	inA := map[string]bool{}
	for _, l := range a {
		inA[l] = true
		if !inB[l] {
			onlyA = append(onlyA, l)
		}
	}
	for _, l := range b {
		if !inA[l] {
			onlyB = append(onlyB, l)
		}
	}
	return onlyA, onlyB
}

// planSQL returns a SQL block from development-plan.md, found by the file
// name comment on its first line (e.g. "001_initial_schema.up.sql").
func planSQL(t testing.TB, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "docs", "development-plan.md"))
	if err != nil {
		t.Fatalf("read the plan: %v", err)
	}
	doc := string(b)
	marker := "```sql\n-- " + file + "\n"
	if n := strings.Count(doc, marker); n != 1 {
		t.Fatalf("development-plan.md has %d SQL blocks headed %q, want 1", n, file)
	}
	start := strings.Index(doc, marker) + len("```sql\n")
	end := strings.Index(doc[start:], "\n```")
	if end < 0 {
		t.Fatalf("SQL block %q in development-plan.md is not closed", file)
	}
	return doc[start : start+end]
}

// connect opens a pool on a database URL from testkit.NewEmptyDB, through
// the same code path the server uses.
func connect(t testing.TB, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// tablesIn lists the ordinary tables in the public schema, sorted.
func tablesIn(t testing.TB, q queryer) []string {
	t.Helper()
	rows, err := q.Query(context.Background(),
		`SELECT relname FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relkind IN ('r', 'p') ORDER BY relname`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return names
}
