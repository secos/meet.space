package testkit

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RaceFunc is one contender in a Race. It runs inside its own transaction
// and returns an error to have that transaction rolled back. It runs on its
// own goroutine, so it must report failures through its return value or a
// variable the test reads afterwards, never with t.Fatal.
type RaceFunc func(ctx context.Context, i int, tx pgx.Tx) error

// Race runs fn as n contenders against the real database at the same time,
// for testing what the schema promises under concurrency: a unique index or
// ON CONFLICT clause that must let exactly one writer win, or a FOR UPDATE
// that must serialize readers.
//
// Before any contender starts, Race acquires n separate connections from
// pool and begins a transaction on each, so all n transactions are open at
// once and none can borrow another's connection. Then it releases every
// goroutine together. A contender whose fn returns nil has its transaction
// committed; one that returns an error is rolled back. The result holds,
// for each contender i, fn's error or the commit error (nil means it
// committed).
//
// Race never sleeps. A contender that blocks on another's uncommitted row
// simply waits for that transaction to end, as it would in production.
// n must be at least 2 and no more than the pool's MaxConns, or the
// connections could not all be held at once.
func Race(t testing.TB, pool *pgxpool.Pool, n int, fn RaceFunc) []error {
	t.Helper()
	if n < 2 {
		t.Fatalf("testkit.Race: need at least 2 contenders, got %d", n)
	}
	if limit := int(pool.Config().MaxConns); n > limit {
		t.Fatalf("testkit.Race: %d contenders but the pool holds at most %d connections", n, limit)
	}
	ctx := t.Context()

	conns := make([]*pgxpool.Conn, 0, n)
	defer func() {
		for _, c := range conns {
			c.Release()
		}
	}()
	txs := make([]pgx.Tx, 0, n)
	for range n {
		c, err := pool.Acquire(ctx)
		if err != nil {
			rollbackAll(ctx, txs)
			t.Fatalf("testkit.Race: acquire connection: %v", err)
		}
		conns = append(conns, c)
		tx, err := c.Begin(ctx)
		if err != nil {
			rollbackAll(ctx, txs)
			t.Fatalf("testkit.Race: begin transaction: %v", err)
		}
		txs = append(txs, tx)
	}

	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i, tx := range txs {
		wg.Go(func() {
			<-start
			if err := fn(ctx, i, tx); err != nil {
				_ = tx.Rollback(ctx)
				errs[i] = err
				return
			}
			errs[i] = tx.Commit(ctx)
		})
	}
	close(start)
	wg.Wait()
	return errs
}

func rollbackAll(ctx context.Context, txs []pgx.Tx) {
	for _, tx := range txs {
		_ = tx.Rollback(ctx)
	}
}
