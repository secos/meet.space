// Package clock is the only place domain code gets the current time from.
// Code takes a Clock and never calls time.Now itself, so tests can set
// any instant (a DST change, a reminder window) and advance it.
package clock

import "time"

// Clock reports the current instant.
type Clock interface {
	Now() time.Time
}

// Real is the wall clock. It is wired only in cmd/server.
type Real struct{}

// Now returns the current UTC time.
func (Real) Now() time.Time { return time.Now().UTC() }
