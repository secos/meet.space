package testkit

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// WantPgError checks that err is a Postgres error with SQLSTATE code that
// names the object the statement ran into:
//
//   - for not-null violations (23502) and writes to a generated column
//     (428C9), the column;
//   - for everything else (unique 23505, foreign key 23503, check 23514, ...),
//     the constraint, which for a unique index is the index's name.
//
// A bare SQLSTATE is not enough for a schema test: a CHECK on another column
// fails with the same 23514 and would pass by accident.
//
// It reports a mismatch with t.Errorf and returns false; it does not stop
// the test, so a table of cases reports every failing row.
func WantPgError(t testing.TB, err error, code, name string) bool {
	t.Helper()
	if err == nil {
		t.Errorf("got no error, want SQLSTATE %s on %q", code, name)
		return false
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		t.Errorf("got %v (%T), want a Postgres error with SQLSTATE %s on %q", err, err, code, name)
		return false
	}
	got, field := PgErrorObject(pe)
	if pe.Code != code || got != name {
		t.Errorf("got SQLSTATE %s on %s %q, want SQLSTATE %s on %q\n  error: %s",
			pe.Code, field, got, code, name, describe(pe))
		return false
	}
	return true
}

// PgErrorObject returns the name WantPgError compares and which field it
// came from ("column" or "constraint").
//
// Postgres sends no column field with 428C9 (seen on Postgres 16: the error
// carries only a message and a detail line), so for that code the column
// is read from the detail, `Column "x" is a generated column.`, which
// Postgres's rewriter emits for both INSERT and UPDATE.
func PgErrorObject(pe *pgconn.PgError) (name, field string) {
	switch pe.Code {
	case pgerrcode.NotNullViolation:
		return pe.ColumnName, "column"
	case pgerrcode.GeneratedAlways:
		if pe.ColumnName != "" {
			return pe.ColumnName, "column"
		}
		var col string
		if _, err := fmt.Sscanf(pe.Detail, "Column %q is a generated column.", &col); err == nil {
			return col, "column"
		}
		return "", "column"
	default:
		return pe.ConstraintName, "constraint"
	}
}

func describe(pe *pgconn.PgError) string {
	s := pe.Message
	if pe.Detail != "" {
		s += " (" + pe.Detail + ")"
	}
	return s
}
