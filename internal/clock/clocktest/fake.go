// Package clocktest provides a settable clock for tests. It is imported
// only by test code, so it is never linked into the server binary.
package clocktest

import (
	"sync"
	"time"
)

// Fake is a Clock whose time moves only when a test says so. It is safe
// for concurrent use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// New returns a Fake set to t.
func New(t time.Time) *Fake { return &Fake{now: t} }

// Now returns the fake's current instant.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

// Advance moves the clock forward by d and returns the new instant.
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}
