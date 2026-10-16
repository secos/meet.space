// Package fakes holds in-memory, recording stand-ins for every outside
// service: the email Sender, object Storage, the CDN, Payments and DNS.
// Tests wire them into the app and then assert on what was sent, stored,
// purged and looked up. None of them touches the network.
//
// Every fake works the same way. Its zero value is ready to use, and it is
// safe for concurrent use.
//
//   - Calls returns every call so far, in the order the calls arrived,
//     with its arguments and (once it has returned) its result. Each call
//     carries a Meta: N (position among all calls to this fake, from 1),
//     Method (the interface method's name), MethodN (position among calls
//     to that method, from 1), Done and Err.
//   - FailNth(n, err) makes the nth call to the fake, counting every
//     method, return err. FailWhen(match, err) makes every call that match
//     accepts return err; match sees the call's arguments and Meta, so it
//     can pick a method, an argument, or the MethodN-th call of a method.
//   - OnCall(hook) runs hook inside every call, after the call is recorded
//     and before it takes effect, with the caller's context. A hook can
//     block (to hold a call open while a test races another one) or
//     inspect the database. If it returns an error, the call returns that
//     error and has no effect.
//
// A call's error is decided in this order: the first hook to return an
// error, then FailNth, then the first FailWhen rule that matches. A failed
// call is recorded but has no effect: nothing is stored, sent or deleted.
//
// Everything a fake returns is a copy; changing it doesn't change the
// fake, and changing a value after passing it to a fake doesn't change
// what the fake recorded.
//
// Only test code imports this package.
package fakes

import (
	"context"
	"slices"
	"sync"

	"meet.space/internal/cdn"
	"meet.space/internal/dnsx"
	"meet.space/internal/email"
	"meet.space/internal/payments"
	"meet.space/internal/storage"
)

var (
	_ email.Sender      = (*Sender)(nil)
	_ storage.Storage   = (*Storage)(nil)
	_ cdn.CDN           = (*CDN)(nil)
	_ payments.Payments = (*Payments)(nil)
	_ dnsx.Resolver     = (*DNS)(nil)
)

// Meta is what every recorded call carries besides its arguments.
type Meta struct {
	// N is the call's position among all calls to this fake, from 1, in
	// the order the calls arrived.
	N int
	// Method is the name of the interface method that was called, such
	// as "Send" or "PurgeURL".
	Method string
	// MethodN is the call's position among calls to the same method,
	// from 1.
	MethodN int
	// Done is false while the call is still running (inside a hook).
	Done bool
	// Err is what the call returned, once Done.
	Err error
}

func (m *Meta) meta() *Meta { return m }

// record is satisfied by a pointer to a call type that embeds Meta.
type record[C any] interface {
	*C
	meta() *Meta
	clone() C
}

type rule[C any] struct {
	match func(C) bool
	err   error
}

// recorder is the machinery every fake embeds: the call log, the failure
// rules and the hooks.
type recorder[C any, P record[C]] struct {
	mu        sync.Mutex
	calls     []C
	perMethod map[string]int
	failNth   map[int]error
	rules     []rule[C]
	hooks     []func(context.Context, C) error
}

// Calls returns every call so far, in arrival order. Calls still running
// have Done false.
func (r *recorder[C, P]) Calls() []C {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]C, len(r.calls))
	for i := range r.calls {
		out[i] = P(&r.calls[i]).clone()
	}
	return out
}

// FailNth makes the nth call to this fake (from 1, counting every method)
// return err.
func (r *recorder[C, P]) FailNth(n int, err error) {
	if n < 1 || err == nil {
		panic("fakes: FailNth needs n >= 1 and a non-nil error")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNth == nil {
		r.failNth = map[int]error{}
	}
	r.failNth[n] = err
}

// FailWhen makes every later call for which match returns true return err.
// match runs outside the fake's lock, so it may call the fake's methods.
func (r *recorder[C, P]) FailWhen(match func(C) bool, err error) {
	if match == nil || err == nil {
		panic("fakes: FailWhen needs a match function and a non-nil error")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules = append(r.rules, rule[C]{match, err})
}

// OnCall adds a hook that runs inside every later call, after the call is
// recorded and before it takes effect. Hooks run in the order they were
// added, outside the fake's lock, so a hook may block or call the fake's
// methods. A hook that returns an error stops the call: the call returns
// that error and has no effect.
func (r *recorder[C, P]) OnCall(hook func(ctx context.Context, c C) error) {
	if hook == nil {
		panic("fakes: OnCall needs a hook")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// do runs one call. It records c on arrival, runs the hooks, applies the
// failure rules and, if nothing failed, runs effect. effect runs under the
// fake's lock, so it may change the fake's state but must not call the
// fake's exported methods; an error from it becomes the call's result.
// do returns the completed call.
func (r *recorder[C, P]) do(ctx context.Context, method string, c C, effect func(*C) error) C {
	r.mu.Lock()
	if r.perMethod == nil {
		r.perMethod = map[string]int{}
	}
	r.perMethod[method]++
	m := P(&c).meta()
	m.N = len(r.calls) + 1
	m.Method = method
	m.MethodN = r.perMethod[method]
	idx := len(r.calls)
	r.calls = append(r.calls, P(&c).clone())
	hooks := slices.Clone(r.hooks)
	r.mu.Unlock()

	var err error
	for _, h := range hooks {
		if err = h(ctx, P(&c).clone()); err != nil {
			break
		}
	}
	if err == nil {
		r.mu.Lock()
		nth, failNth := r.failNth[m.N]
		rules := slices.Clone(r.rules)
		r.mu.Unlock()
		if failNth {
			err = nth
		}
		for _, rl := range rules {
			if err != nil {
				break
			}
			if rl.match(P(&c).clone()) {
				err = rl.err
			}
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil && effect != nil {
		err = effect(&c)
	}
	m.Err = err
	m.Done = true
	r.calls[idx] = P(&c).clone()
	return P(&c).clone()
}
