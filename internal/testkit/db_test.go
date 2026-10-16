package testkit_test

import (
	"context"
	"sync"
	"testing"

	"meet.space/internal/testkit"
)

// Each test gets its own database: rows written in one are invisible in
// another, even when the tests run in parallel and insert the same email.
func TestNewDB_IsolatedPerTest(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	names := map[string]bool{}
	for i := range 3 {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			db := testkit.NewDB(t)
			mu.Lock()
			if names[db.Name] {
				t.Errorf("database %s handed out twice", db.Name)
			}
			names[db.Name] = true
			mu.Unlock()
			ctx := context.Background()
			if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email) VALUES (gen_random_uuid(), 'same@example.com')`); err != nil {
				t.Fatalf("insert %d: %v", i, err)
			}
			var n int
			if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("database sees %d users, want only its own 1", n)
			}
		})
	}
}
