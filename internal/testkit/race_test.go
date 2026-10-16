package testkit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"

	"meet.space/internal/testkit"
)

// Every contender has its own backend and all of their transactions are
// open at the same moment: each contender inserts a row and then waits,
// inside its open transaction, until every other contender has inserted
// too. If Race ran them one after another, or two shared a connection, the
// wait could never finish. Afterwards every row is visible, so each
// transaction committed. P0-SCHEMA helper (race.go).
func TestRace_ContendersHoldOpenTransactionsOnSeparateConnectionsAtOnce(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE race_probe (i int PRIMARY KEY, pid int NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	const n = 4
	// A deadline only turns a hang into a failure; nothing waits on time.
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	arrived := make(chan struct{}, n)
	allIn := make(chan struct{})
	go func() {
		for range n {
			select {
			case <-arrived:
			case <-wait.Done():
				return
			}
		}
		close(allIn)
	}()

	errs := testkit.Race(t, db.Pool, n, func(ctx context.Context, i int, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO race_probe (i, pid) VALUES ($1, pg_backend_pid())`, i); err != nil {
			return err
		}
		arrived <- struct{}{}
		select {
		case <-allIn:
			return nil
		case <-wait.Done():
			return fmt.Errorf("contender %d: the others never had a transaction open at the same time", i)
		}
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("contender %d: %v", i, err)
		}
	}
	var rows, pids int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT pid) FROM race_probe`).Scan(&rows, &pids); err != nil {
		t.Fatal(err)
	}
	if rows != n || pids != n {
		t.Errorf("committed %d rows from %d backends, want %d from %d", rows, pids, n, n)
	}
}

// A contender that returns an error is rolled back and its error is
// reported at its own index; the others still commit.
// P0-SCHEMA helper (race.go).
func TestRace_RollsBackOnlyTheContendersThatFail(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE race_probe (i int PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	errOdd := errors.New("odd contender gives up")
	errs := testkit.Race(t, db.Pool, 4, func(ctx context.Context, i int, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO race_probe (i) VALUES ($1)`, i); err != nil {
			return err
		}
		if i%2 == 1 {
			return errOdd
		}
		return nil
	})
	for i, err := range errs {
		want := error(nil)
		if i%2 == 1 {
			want = errOdd
		}
		if !errors.Is(err, want) {
			t.Errorf("contender %d: got %v, want %v", i, err, want)
		}
	}
	rows, err := db.Pool.Query(ctx, `SELECT i FROM race_probe ORDER BY i`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[0 2]" {
		t.Errorf("committed rows %v, want [0 2]", got)
	}
}

// Contenders writing the same key into a unique index: exactly one wins and
// every other one sees the unique violation, whatever order the database
// runs them in. P0-SCHEMA helper (race.go).
func TestRace_UniqueKeyHasExactlyOneWinner(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE race_probe (k int PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	errs := testkit.Race(t, db.Pool, 4, func(ctx context.Context, _ int, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO race_probe (k) VALUES (1)`)
		return err
	})
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		testkit.WantPgError(t, err, pgerrcode.UniqueViolation, "race_probe_pkey")
	}
	if wins != 1 {
		t.Errorf("%d contenders committed, want exactly 1", wins)
	}
}

// Contenders that read a row FOR UPDATE and write it back serialize on the
// row lock, so no increment is lost. P0-SCHEMA helper (race.go).
func TestRace_ForUpdateSerializesReadModifyWrite(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE race_counter (id int PRIMARY KEY, n int NOT NULL); INSERT INTO race_counter VALUES (1, 0)`); err != nil {
		t.Fatal(err)
	}
	const n = 4
	errs := testkit.Race(t, db.Pool, n, func(ctx context.Context, _ int, tx pgx.Tx) error {
		var v int
		if err := tx.QueryRow(ctx, `SELECT n FROM race_counter WHERE id = 1 FOR UPDATE`).Scan(&v); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE race_counter SET n = $1 WHERE id = 1`, v+1)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("contender %d: %v", i, err)
		}
	}
	var got int
	if err := db.Pool.QueryRow(ctx, `SELECT n FROM race_counter WHERE id = 1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Errorf("counter is %d after %d locked increments, want %d", got, n, n)
	}
}

// Asking for more contenders than the pool can hold at once is a test bug,
// reported before anything runs, rather than a deadlock.
// P0-SCHEMA helper (race.go).
func TestRace_RefusesMoreContendersThanConnections(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	limit := int(db.Pool.Config().MaxConns)
	ran := false
	r := record(t, func(tb testing.TB) {
		testkit.Race(tb, db.Pool, limit+1, func(context.Context, int, pgx.Tx) error {
			ran = true
			return nil
		})
	})
	if !r.fatal {
		t.Error("Race did not stop the test")
	}
	if ran {
		t.Error("a contender ran")
	}
}
