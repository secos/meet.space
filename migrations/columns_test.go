package migrations_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"

	"meet.space/internal/testkit"
)

// P0-SCHEMA-06
// No UUID primary key has a database default: ids come only from Go's
// uuid.NewV7(), so a row inserted without one is refused on the id column,
// in each of the 13 tables keyed by id.
func TestUUIDPrimaryKeys_HaveNoDatabaseDefault(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	var got []string
	rows, err := f.q.Query(f.ctx, `SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'id' AND data_type = 'uuid' AND column_default IS NULL
		ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	got, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, idTables) {
		t.Errorf("uuid id columns without a default: %v, want %v", got, idTables)
	}
	for _, table := range idTables {
		t.Run(table, func(t *testing.T) {
			f := f.on(t)
			err := f.insert(table, f.row(table).without("id"))
			testkit.WantPgError(t, err, pgerrcode.NotNullViolation, "id")
		})
	}
}

// P0-SCHEMA-06
// IDs made by uuid.NewV7() are version 7 and sort in the order they were
// made, both as Go bytes and under Postgres's ORDER BY id, so ORDER BY id
// is chronological as the plan says.
func TestUUIDv7IDs_SortInCreationOrder(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	const n = 300
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = newID(t)
		if ids[i].Version() != 7 {
			t.Fatalf("id %s is version %d, want 7", ids[i], ids[i].Version())
		}
		if i > 0 && bytes.Compare(ids[i-1][:], ids[i][:]) >= 0 {
			t.Fatalf("id %d (%s) does not sort after id %d (%s)", i, ids[i], i-1, ids[i-1])
		}
	}
	shuffled := slices.Clone(ids)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	for _, id := range shuffled {
		f.add("users", "id", id)
	}
	rows, err := f.q.Query(f.ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, ids) {
		t.Error("ORDER BY id does not return users in the order their ids were made")
	}
}

// timestampColumns lists every column whose type is a date or time type.
func timestampColumns(t *testing.T, f *fx) (cols [][2]string, types map[[2]string]string) {
	t.Helper()
	rows, err := f.q.Query(f.ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
		  AND (data_type LIKE 'timestamp%' OR data_type LIKE 'time %' OR data_type IN ('date', 'interval'))
		ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatal(err)
	}
	types = map[[2]string]string{}
	for rows.Next() {
		var table, col, typ string
		if err := rows.Scan(&table, &col, &typ); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, [2]string{table, col})
		types[[2]string{table, col}] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return cols, types
}

// P0-SCHEMA-07
// Every time column is TIMESTAMPTZ (no bare TIMESTAMP, DATE or TIME), and
// every *_at column is one of them.
func TestTimestampColumns_AreAllTimestamptz(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	cols, types := timestampColumns(t, f)
	if len(cols) == 0 {
		t.Fatal("no time columns found")
	}
	for _, c := range cols {
		if typ := types[c]; typ != "timestamp with time zone" {
			t.Errorf("%s.%s is %s, want timestamp with time zone", c[0], c[1], typ)
		}
	}
	rows, err := f.q.Query(f.ctx, `SELECT table_name || '.' || column_name || ' ' || data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name LIKE '%\_at' AND data_type <> 'timestamp with time zone'`)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("*_at column is not timestamptz: %s", b)
	}
}

// P0-SCHEMA-07
// An instant written and read back under a non-UTC session time zone is
// unchanged, in every time column. The two instants are the same wall-clock
// time in Chicago (01:30 on the night daylight saving ends, once in CDT and
// once in CST), so a column that stored local time would merge them. Each
// is also read from a UTC session and as text in UTC, so the stored value
// does not depend on the driver's decoding.
func TestTimestamps_RoundTripUnchangedUnderNonUTCSessionTimeZone(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	instants := []time.Time{
		time.Date(2026, 11, 1, 6, 30, 0, 123456000, time.UTC), // 01:30 CDT
		time.Date(2026, 11, 1, 7, 30, 0, 654321000, time.UTC), // 01:30 CST
	}
	cols, _ := timestampColumns(t, f)
	for _, c := range cols {
		table, col := c[0], c[1]
		t.Run(table+"."+col, func(t *testing.T) {
			f := f.on(t)
			for _, want := range instants {
				r := f.row(table, col, want)
				ident := pgx.Identifier{col}.Sanitize()
				sql, args := insertSQL(table, r, fmt.Sprintf("RETURNING t.%s, (t.%s AT TIME ZONE 'UTC')::text", ident, ident))
				tx, err := f.q.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(f.ctx, `SET LOCAL TimeZone = 'America/Chicago'`); err != nil {
					t.Fatal(err)
				}
				var got time.Time
				var utcText string
				if err := tx.QueryRow(f.ctx, sql, args...).Scan(&got, &utcText); err != nil {
					t.Fatalf("insert: %v", err)
				}
				if err := tx.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				if !got.Equal(want) {
					t.Errorf("read back in America/Chicago as %s, want %s", got.UTC(), want)
				}
				if wantText := want.Format("2006-01-02 15:04:05.000000"); utcText != wantText {
					t.Errorf("stored as %s UTC, want %s", utcText, wantText)
				}
				var where []string
				var keyArgs []any
				for i, k := range primaryKey(table) {
					where = append(where, fmt.Sprintf("%s = $%d", pgx.Identifier{k}.Sanitize(), i+1))
					keyArgs = append(keyArgs, r[k])
				}
				var fromUTC time.Time
				f.scan(fmt.Sprintf("SELECT %s FROM %s WHERE %s", ident, pgx.Identifier{table}.Sanitize(), strings.Join(where, " AND ")), keyArgs, &fromUTC)
				if !fromUTC.Equal(want) {
					t.Errorf("read back in UTC as %s, want %s", fromUTC.UTC(), want)
				}
			}
		})
	}
}

// P0-SCHEMA-08
// Every NOT NULL column refuses an explicit NULL, naming that column; one
// without a default also refuses a row that leaves it out.
func TestNotNullColumns_RejectNull(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	rows, err := f.q.Query(f.ctx, `SELECT c.relname, a.attname, d.adbin IS NOT NULL
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r' AND c.relname <> 'schema_migrations'
		  AND a.attnum > 0 AND NOT a.attisdropped AND a.attnotnull AND a.attgenerated = ''
		ORDER BY c.relname, a.attnum`)
	if err != nil {
		t.Fatal(err)
	}
	type col struct {
		table, name string
		hasDefault  bool
	}
	cols, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (col, error) {
		var c col
		err := r.Scan(&c.table, &c.name, &c.hasDefault)
		return c, err
	})
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, c := range cols {
		covered[c.table] = true
		t.Run(c.table+"."+c.name, func(t *testing.T) {
			f := f.on(t)
			testkit.WantPgError(t, f.insert(c.table, f.row(c.table).with(c.name, nil)), pgerrcode.NotNullViolation, c.name)
			if !c.hasDefault {
				testkit.WantPgError(t, f.insert(c.table, f.row(c.table).without(c.name)), pgerrcode.NotNullViolation, c.name)
			}
		})
	}
	for _, table := range appTables {
		if !covered[table] {
			t.Errorf("no NOT NULL column found in %s", table)
		}
	}
}

// foreignKeys are the plan's 20 references: the referencing columns in key
// order (the first is the one the test breaks) and the constraint name.
// Two are composite (decisions.md "Reply rules in the database").
var foreignKeys = []struct{ table, columns, constraint string }{
	{"attachments", "group_id", "attachments_group_id_fkey"},
	{"attachments", "post_id", "attachments_post_id_fkey"},
	{"email_events", "group_id", "email_events_group_id_fkey"},
	{"email_events", "outbox_id", "email_events_outbox_id_fkey"},
	{"email_opt_outs", "group_id", "email_opt_outs_group_id_fkey"},
	{"email_outbox", "group_id", "email_outbox_group_id_fkey"},
	{"meetings", "group_id", "meetings_group_id_fkey"},
	{"memberships", "group_id", "memberships_group_id_fkey"},
	{"memberships", "user_id", "memberships_user_id_fkey"},
	{"notification_preferences", "group_id", "notification_preferences_group_id_fkey"},
	{"notification_preferences", "user_id", "notification_preferences_user_id_fkey"},
	{"posts", "group_id", "posts_group_id_fkey"},
	{"posts", "meeting_id,group_id", "posts_meeting_fkey"},
	{"posts", "parent_id,group_id,is_reply", "posts_parent_fkey"},
	{"posts", "user_id", "posts_user_id_fkey"},
	{"rsvps", "meeting_id", "rsvps_meeting_id_fkey"},
	{"rsvps", "user_id", "rsvps_user_id_fkey"},
	{"sessions", "user_id", "sessions_user_id_fkey"},
	{"subscriptions", "group_id", "subscriptions_group_id_fkey"},
	{"subscriptions", "payer_user_id", "subscriptions_payer_user_id_fkey"},
}

// P0-SCHEMA-09
// Every foreign key refuses a reference to a row that doesn't exist, on
// insert and on update, naming the constraint.
func TestForeignKeys_RejectDanglingReferences(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	rows, err := f.q.Query(f.ctx, `SELECT cl.relname || '.' || array_to_string(ARRAY(
			SELECT a.attname FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
			JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
			ORDER BY k.ord), ',') || ' ' || con.conname
		FROM pg_constraint con
		JOIN pg_class cl ON cl.oid = con.conrelid
		WHERE con.contype = 'f' AND con.connamespace = 'public'::regnamespace
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	inCatalog, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(inCatalog)
	var listed []string
	for _, fk := range foreignKeys {
		listed = append(listed, fk.table+"."+fk.columns+" "+fk.constraint)
	}
	slices.Sort(listed)
	if !slices.Equal(inCatalog, listed) {
		t.Errorf("foreign keys in the catalog:\n  %s\nwant:\n  %s", strings.Join(inCatalog, "\n  "), strings.Join(listed, "\n  "))
	}
	for _, fk := range foreignKeys {
		column, _, _ := strings.Cut(fk.columns, ",")
		t.Run(fk.table+"."+column, func(t *testing.T) {
			f := f.on(t)
			missing := newID(t)
			testkit.WantPgError(t, f.insert(fk.table, f.row(fk.table, column, missing)), pgerrcode.ForeignKeyViolation, fk.constraint)

			r := f.add(fk.table)
			var where []string
			args := []any{missing}
			for i, k := range primaryKey(fk.table) {
				where = append(where, fmt.Sprintf("%s = $%d", k, i+2))
				args = append(args, r[k])
			}
			err := f.try(fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s", fk.table, column, strings.Join(where, " AND ")), args...)
			testkit.WantPgError(t, err, pgerrcode.ForeignKeyViolation, fk.constraint)
		})
	}
}

// emailColumns are the plan's six email address columns and their
// lowercase CHECK constraints.
var emailColumns = []struct{ table, column, constraint string }{
	{"email_opt_outs", "email", "email_opt_outs_email_check"},
	{"email_outbox", "to_email", "email_outbox_to_email_check"},
	{"email_suppressions", "email", "email_suppressions_email_check"},
	{"magic_links", "email", "magic_links_email_check"},
	{"rsvps", "email", "rsvps_email_check"},
	{"users", "email", "users_email_check"},
}

// P0-SCHEMA-10
// Every email column stores lowercase only: 'Ann@Example.com' is refused by
// that column's CHECK, on insert and on update, and 'ann@example.com' is
// accepted. The list of columns is checked against the catalog, so a new
// email column without the CHECK fails here.
func TestEmailColumns_RejectUppercase(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	rows, err := f.q.Query(f.ctx, `SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND (column_name = 'email' OR column_name LIKE '%\_email')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	inCatalog, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, c := range emailColumns {
		listed = append(listed, c.table+"."+c.column)
	}
	if !slices.Equal(inCatalog, listed) {
		t.Errorf("email columns in the catalog %v, want %v", inCatalog, listed)
	}
	for _, c := range emailColumns {
		t.Run(c.table+"."+c.column, func(t *testing.T) {
			f := f.on(t)
			testkit.WantPgError(t, f.insert(c.table, f.row(c.table, c.column, "Ann@Example.com")), pgerrcode.CheckViolation, c.constraint)
			r := f.add(c.table, c.column, "ann@example.com")
			var where []string
			var args []any
			for i, k := range primaryKey(c.table) {
				where = append(where, fmt.Sprintf("%s = $%d", k, i+1))
				args = append(args, r[k])
			}
			err := f.try(fmt.Sprintf("UPDATE %s SET %s = 'ANN@example.com' WHERE %s", c.table, c.column, strings.Join(where, " AND ")), args...)
			testkit.WantPgError(t, err, pgerrcode.CheckViolation, c.constraint)
		})
	}
}

// P0-SCHEMA-11
// A group's custom domain is stored lowercase: NULL (no domain) and
// 'hikers.localhost:8080' are accepted, 'Hikers.Example.com' is refused.
func TestGroupCustomDomain_RejectsUppercase(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	for _, ok := range []any{nil, "hikers.localhost:8080"} {
		if err := f.insert("groups", f.row("groups", "custom_domain", ok)); err != nil {
			t.Errorf("custom_domain %v refused: %v", ok, err)
		}
	}
	testkit.WantPgError(t, f.insert("groups", f.row("groups", "custom_domain", "Hikers.Example.com")),
		pgerrcode.CheckViolation, "groups_custom_domain_check")
}

// P0-SCHEMA-12
// Security Rule 1 (return paths), database half: a magic link's
// redirect_path is NULL or a relative path starting with a single "/".
// Absolute URLs, scheme-relative "//host" and "///host", other schemes,
// bare words, a leading space and the empty string are refused by the
// CHECK.
// OPEN (G-20): the documents don't say whether paths with a backslash
// ("/\evil.com") or control characters must be refused here; they are not
// tested.
func TestRule1_RedirectPathCheckAcceptsOnlySingleSlashRelativePaths(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	for _, ok := range []any{nil, "/", "/admin", "/groups/x?y=1#z"} {
		if err := f.insert("magic_links", f.row("magic_links", "redirect_path", ok)); err != nil {
			t.Errorf("redirect_path %#v refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "admin", " /admin", "https://evil.com", "javascript:alert(1)", "//evil.com", "///evil.com"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			f := f.on(t)
			testkit.WantPgError(t, f.insert("magic_links", f.row("magic_links", "redirect_path", bad)),
				pgerrcode.CheckViolation, "magic_links_redirect_path_check")
		})
	}
}
