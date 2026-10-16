package testkit_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgerrcode"

	"meet.space/internal/testkit"
)

// recTB stands in for *testing.T when a test checks what a helper reports.
// Errorf and Fatalf are recorded instead of failing the real test; Fatalf
// and FailNow end the calling goroutine, as the real ones do.
type recTB struct {
	testing.TB
	mu    sync.Mutex
	msgs  []string
	fatal bool
}

func (r *recTB) Helper() {}

func (r *recTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func (r *recTB) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	r.FailNow()
}

func (r *recTB) FailNow() {
	r.mu.Lock()
	r.fatal = true
	r.mu.Unlock()
	runtime.Goexit()
}

// record calls fn with a recorder on its own goroutine, so a Fatalf inside
// fn ends only that goroutine.
func record(t *testing.T, fn func(tb testing.TB)) *recTB {
	t.Helper()
	r := &recTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r
}

func (r *recTB) reported() string { return strings.Join(r.msgs, "\n") }

// pgErrorCase produces a real Postgres error from the schema.
type pgErrorCase struct {
	name string
	sql  string
	code string
	obj  string // the column or constraint the error must name
}

// Each kind of error the schema tests rely on is recognized by its SQLSTATE
// and by the object it names, which comes from the field Postgres fills
// for that kind (the column for 23502 and 428C9, the constraint otherwise).
// P0-SCHEMA helper (pgerr.go).
func TestWantPgError_MatchesRealErrorsByCodeAndObject(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email) VALUES ('00000000-0000-7000-8000-000000000001', 'ann@example.com')`); err != nil {
		t.Fatal(err)
	}
	cases := []pgErrorCase{
		{"unique", `INSERT INTO users (id, email) VALUES ('00000000-0000-7000-8000-000000000002', 'ann@example.com')`, pgerrcode.UniqueViolation, "users_email_key"},
		{"check", `INSERT INTO users (id, email) VALUES ('00000000-0000-7000-8000-000000000003', 'Bob@example.com')`, pgerrcode.CheckViolation, "users_email_check"},
		{"not null", `INSERT INTO users (id) VALUES ('00000000-0000-7000-8000-000000000004')`, pgerrcode.NotNullViolation, "email"},
		{"foreign key", `INSERT INTO sessions (id, user_id, host, token_hash, expires_at) VALUES ('00000000-0000-7000-8000-000000000005', '00000000-0000-7000-8000-00000000ffff', 'localhost:8080', 'h', now())`, pgerrcode.ForeignKeyViolation, "sessions_user_id_fkey"},
		{"generated column on insert", `INSERT INTO groups (id, slug, name, timezone, search_vector) VALUES ('00000000-0000-7000-8000-000000000006', 's', 'n', 'UTC', to_tsvector('x'))`, pgerrcode.GeneratedAlways, "search_vector"},
		{"generated column on update", `UPDATE groups SET search_vector = to_tsvector('x')`, pgerrcode.GeneratedAlways, "search_vector"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := db.Pool.Exec(ctx, c.sql)
			r := record(t, func(tb testing.TB) {
				if !testkit.WantPgError(tb, err, c.code, c.obj) {
					tb.Errorf("returned false")
				}
			})
			if len(r.msgs) != 0 {
				t.Errorf("WantPgError(%v, %s, %q) reported:\n%s", err, c.code, c.obj, r.reported())
			}
		})
	}
}

// A wrong SQLSTATE, a wrong object, no error at all, or an error that is
// not from Postgres must each be reported and return false, so a schema
// test can't pass on the wrong failure. P0-SCHEMA helper (pgerr.go).
func TestWantPgError_ReportsMismatches(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	ctx := context.Background()
	_, checkErr := db.Pool.Exec(ctx, `INSERT INTO users (id, email) VALUES ('00000000-0000-7000-8000-000000000001', 'Ann@example.com')`)
	_, nullErr := db.Pool.Exec(ctx, `INSERT INTO users (id) VALUES ('00000000-0000-7000-8000-000000000002')`)
	cases := []struct {
		name string
		err  error
		code string
		obj  string
		want string // a fragment the report must contain
	}{
		{"wrong code", checkErr, pgerrcode.UniqueViolation, "users_email_check", "SQLSTATE 23514"},
		{"wrong constraint", checkErr, pgerrcode.CheckViolation, "users_name_check", `"users_email_check"`},
		{"wrong column", nullErr, pgerrcode.NotNullViolation, "name", `column "email"`},
		{"no error", nil, pgerrcode.CheckViolation, "users_email_check", "got no error"},
		{"not a Postgres error", errors.New("boom"), pgerrcode.CheckViolation, "users_email_check", "boom"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ok bool
			r := record(t, func(tb testing.TB) { ok = testkit.WantPgError(tb, c.err, c.code, c.obj) })
			if ok {
				t.Error("returned true for a mismatch")
			}
			if !strings.Contains(r.reported(), c.want) {
				t.Errorf("report %q does not mention %q", r.reported(), c.want)
			}
			if r.fatal {
				t.Error("stopped the test; it should only report")
			}
		})
	}
}
