package fakes_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"meet.space/internal/email"
	"meet.space/internal/payments"
	"meet.space/internal/storage"
	"meet.space/internal/testkit"
	"meet.space/internal/testkit/fakes"
)

// Importing testkit installs the network guard for this test binary.
var _ = testkit.ErrNetworkInTest

var errBoom = errors.New("boom")

// checkLog asserts that a fake's call log is complete and ordered: the
// ith call has N = i+1, every call has returned, and within each method
// MethodN runs 1, 2, 3, ... in log order. It returns the count per method.
func checkLog(t *testing.T, name string, metas []fakes.Meta) map[string]int {
	t.Helper()
	per := map[string]int{}
	for i, m := range metas {
		if m.N != i+1 {
			t.Fatalf("%s: call %d has N=%d, want %d", name, i, m.N, i+1)
		}
		if !m.Done {
			t.Fatalf("%s: call %d (%s) not done after every caller returned", name, m.N, m.Method)
		}
		per[m.Method]++
		if m.MethodN != per[m.Method] {
			t.Fatalf("%s: call %d is %s #%d, want #%d", name, m.N, m.Method, m.MethodN, per[m.Method])
		}
	}
	return per
}

func metas[C any](calls []C, meta func(C) fakes.Meta) []fakes.Meta {
	out := make([]fakes.Meta, len(calls))
	for i, c := range calls {
		out[i] = meta(c)
	}
	return out
}

// Many goroutines calling every fake at once, while others read the logs
// and add rules and hooks, lose no call, number each one exactly once and
// keep the log in arrival order. Run with -race.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_ConcurrentUseRecordsEveryCallOnce(t *testing.T) {
	const goroutines, perG = 16, 40
	ctx := context.Background()
	sender := &fakes.Sender{}
	store := &fakes.Storage{}
	cdnf := &fakes.CDN{}
	pay := &fakes.Payments{}
	dns := &fakes.DNS{}
	dns.SetCNAME("hikers.example.com", "zone.b-cdn.net.")

	never := errors.New("never matches")
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			for k := range perG {
				key := fmt.Sprintf("groups/%d/%d", g, k)
				if _, err := sender.Send(ctx, email.Message{To: key}); err != nil {
					t.Error(err)
				}
				if err := store.Put(ctx, key, strings.NewReader(key), int64(len(key)), "text/plain"); err != nil {
					t.Error(err)
				}
				if _, _, err := store.Get(ctx, key); err != nil {
					t.Error(err)
				}
				if err := cdnf.PurgeURL(ctx, "https://meet.space/"+key); err != nil {
					t.Error(err)
				}
				if err := cdnf.AddHostname(ctx, key); err != nil {
					t.Error(err)
				}
				if _, err := pay.CreateCheckoutSession(ctx, payments.CheckoutParams{PriceID: key}); err != nil {
					t.Error(err)
				}
				if err := pay.CancelSubscription(ctx, key); err != nil {
					t.Error(err)
				}
				if _, err := dns.LookupCNAME(ctx, "hikers.example.com"); err != nil {
					t.Error(err)
				}
				_, _ = dns.LookupMX(ctx, key) // not found; still recorded
			}
		})
	}
	// Readers and rule changes race with the callers.
	wg.Go(func() {
		for range perG {
			_ = sender.Sent()
			_ = store.Keys()
			_ = cdnf.Purged()
			_ = pay.Calls()
			_ = dns.Calls()
			sender.OnCall(func(context.Context, fakes.SenderCall) error { return nil })
			store.FailWhen(func(fakes.StorageCall) bool { return false }, never)
			dns.SetCNAME("other.example.com", "zone.b-cdn.net.")
		}
	})
	wg.Wait()

	n := goroutines * perG
	want := func(name string, got map[string]int, methods ...string) {
		t.Helper()
		for _, m := range methods {
			if got[m] != n {
				t.Errorf("%s: %d %s calls recorded, want %d", name, got[m], m, n)
			}
		}
	}
	want("Sender", checkLog(t, "Sender", metas(sender.Calls(), func(c fakes.SenderCall) fakes.Meta { return c.Meta })), "Send")
	want("Storage", checkLog(t, "Storage", metas(store.Calls(), func(c fakes.StorageCall) fakes.Meta { return c.Meta })), "Put", "Get")
	want("CDN", checkLog(t, "CDN", metas(cdnf.Calls(), func(c fakes.CDNCall) fakes.Meta { return c.Meta })), "PurgeURL", "AddHostname")
	want("Payments", checkLog(t, "Payments", metas(pay.Calls(), func(c fakes.PaymentsCall) fakes.Meta { return c.Meta })), "CreateCheckoutSession", "CancelSubscription")
	want("DNS", checkLog(t, "DNS", metas(dns.Calls(), func(c fakes.DNSCall) fakes.Meta { return c.Meta })), "LookupCNAME", "LookupMX")

	ids := map[string]bool{}
	for _, c := range sender.Calls() {
		ids[c.MessageID] = true
	}
	if len(ids) != n {
		t.Errorf("%d distinct message IDs for %d sends", len(ids), n)
	}
	if got := len(store.Keys()); got != n {
		t.Errorf("%d objects stored, want %d", got, n)
	}
	if got := len(cdnf.Purged()); got != n {
		t.Errorf("%d purges, want %d", got, n)
	}
}

// Calls to different methods share one sequence (N), and each method has
// its own (MethodN), both in the order the calls were made.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_CallsAreRecordedInOrderAcrossMethods(t *testing.T) {
	ctx := context.Background()
	f := &fakes.CDN{}
	_ = f.AddHostname(ctx, "hikers.example.com")
	_ = f.PurgeURL(ctx, "https://meet.space/groups/hikers")
	_ = f.LoadFreeCertificate(ctx, "hikers.example.com")
	_ = f.PurgeURL(ctx, "https://hikers.example.com/")
	_ = f.RemoveHostname(ctx, "hikers.example.com")

	want := []fakes.CDNCall{
		{Meta: fakes.Meta{N: 1, Method: "AddHostname", MethodN: 1, Done: true}, Host: "hikers.example.com"},
		{Meta: fakes.Meta{N: 2, Method: "PurgeURL", MethodN: 1, Done: true}, URL: "https://meet.space/groups/hikers"},
		{Meta: fakes.Meta{N: 3, Method: "LoadFreeCertificate", MethodN: 1, Done: true}, Host: "hikers.example.com"},
		{Meta: fakes.Meta{N: 4, Method: "PurgeURL", MethodN: 2, Done: true}, URL: "https://hikers.example.com/"},
		{Meta: fakes.Meta{N: 5, Method: "RemoveHostname", MethodN: 1, Done: true}, Host: "hikers.example.com"},
	}
	got := f.Calls()
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d:\n got %+v\nwant %+v", i+1, got[i], want[i])
		}
	}
}

// FailNth counts every call to the fake, whatever the method.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_FailNthCountsEveryMethod(t *testing.T) {
	ctx := context.Background()
	f := &fakes.CDN{}
	f.FailNth(3, errBoom)
	errs := []error{
		f.AddHostname(ctx, "a.example.com"),
		f.PurgeURL(ctx, "https://meet.space/a"),
		f.LoadFreeCertificate(ctx, "a.example.com"), // 3rd call
		f.LoadFreeCertificate(ctx, "a.example.com"),
	}
	for i, err := range errs {
		if wantErr := i == 2; (err != nil) != wantErr || (wantErr && !errors.Is(err, errBoom)) {
			t.Errorf("call %d returned %v", i+1, err)
		}
	}
	calls := f.Calls()
	if !errors.Is(calls[2].Err, errBoom) || calls[3].Err != nil {
		t.Errorf("recorded errors: %v, %v", calls[2].Err, calls[3].Err)
	}
}

// FailWhen sees the call's method, arguments and per-method position, so
// a test can fail exactly the second purge, or every call for one host.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_FailWhenPicksCallsByMethodArgumentOrPosition(t *testing.T) {
	ctx := context.Background()
	errCert := errors.New("certificate failed")
	f := &fakes.CDN{}
	f.FailWhen(func(c fakes.CDNCall) bool { return c.Method == "PurgeURL" && c.MethodN == 2 }, errBoom)
	f.FailWhen(func(c fakes.CDNCall) bool { return c.Method == "LoadFreeCertificate" && c.Host == "bad.example.com" }, errCert)

	if err := f.PurgeURL(ctx, "https://meet.space/1"); err != nil {
		t.Errorf("purge 1: %v", err)
	}
	if err := f.PurgeURL(ctx, "https://meet.space/2"); !errors.Is(err, errBoom) {
		t.Errorf("purge 2: %v, want boom", err)
	}
	if err := f.PurgeURL(ctx, "https://meet.space/3"); err != nil {
		t.Errorf("purge 3: %v", err)
	}
	if err := f.LoadFreeCertificate(ctx, "good.example.com"); err != nil {
		t.Errorf("good host: %v", err)
	}
	for range 2 {
		if err := f.LoadFreeCertificate(ctx, "bad.example.com"); !errors.Is(err, errCert) {
			t.Errorf("bad host: %v, want certificate failed", err)
		}
	}
	if got, want := f.Purged(), []string{"https://meet.space/1", "https://meet.space/3"}; !slices.Equal(got, want) {
		t.Errorf("Purged() = %q, want %q (a failed purge purged nothing)", got, want)
	}
}

// When several failures apply, the first hook error wins, then FailNth,
// then the first matching FailWhen rule. Hooks run even on a call that
// will fail.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_FailurePrecedenceAndHooksOnFailingCalls(t *testing.T) {
	ctx := context.Background()
	errHook, errNth, errRule1, errRule2 := errors.New("hook"), errors.New("nth"), errors.New("rule1"), errors.New("rule2")
	f := &fakes.CDN{}
	var hooked []int
	f.OnCall(func(_ context.Context, c fakes.CDNCall) error {
		hooked = append(hooked, c.N)
		if c.N == 1 {
			return errHook
		}
		return nil
	})
	f.FailNth(1, errNth)
	f.FailNth(2, errNth)
	f.FailWhen(func(fakes.CDNCall) bool { return true }, errRule1)
	f.FailWhen(func(fakes.CDNCall) bool { return true }, errRule2)

	for i, want := range []error{errHook, errNth, errRule1} {
		if err := f.PurgeURL(ctx, "https://meet.space/"); !errors.Is(err, want) {
			t.Errorf("call %d: %v, want %v", i+1, err, want)
		}
	}
	if !slices.Equal(hooked, []int{1, 2, 3}) {
		t.Errorf("hook ran for calls %v, want [1 2 3]", hooked)
	}
}

// A hook that blocks holds the call open: it is in the log, not done, and
// has had no effect until the hook returns. The hook can read the fake
// while it waits.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_HookCanHoldACallOpen(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Sender{}
	entered, release := make(chan fakes.SenderCall), make(chan struct{})
	var inHook []fakes.SenderCall
	s.OnCall(func(_ context.Context, c fakes.SenderCall) error {
		inHook = s.Calls() // must not deadlock
		entered <- c
		<-release
		return nil
	})

	type result struct {
		id  string
		err error
	}
	done := make(chan result)
	go func() {
		id, err := s.Send(ctx, email.Message{To: "ann@example.com"})
		done <- result{id, err}
	}()
	c := wait(t, entered)
	if c.N != 1 || c.Done || c.Message.To != "ann@example.com" {
		t.Errorf("hook saw %+v", c)
	}
	if calls := s.Calls(); len(calls) != 1 || calls[0].Done {
		t.Errorf("while held: Calls() = %+v, want one call not done", calls)
	}
	if len(inHook) != 1 || inHook[0].Done {
		t.Errorf("inside the hook: Calls() = %+v, want one call not done", inHook)
	}
	if sent := s.Sent(); len(sent) != 0 {
		t.Errorf("while held: Sent() = %+v, want nothing", sent)
	}
	close(release)
	r := wait(t, done)
	if r.err != nil || r.id != "fake-1" {
		t.Errorf("Send returned %q, %v", r.id, r.err)
	}
	if calls := s.Calls(); !calls[0].Done || calls[0].MessageID != "fake-1" {
		t.Errorf("after release: %+v", calls[0])
	}
}

// A hook runs before the call takes effect and gets the caller's context.
// An error from it fails the call, which then has no effect.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_HookRunsBeforeTheEffectWithTheCallersContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "request-7")
	s := &fakes.Storage{}
	var sawValue any
	var storedDuringHook bool
	s.OnCall(func(ctx context.Context, c fakes.StorageCall) error {
		sawValue = ctx.Value(ctxKey{})
		_, _, storedDuringHook = s.Object(c.Key)
		if c.Key == "groups/g/refused" {
			return errBoom
		}
		return nil
	})
	if err := s.Put(ctx, "groups/g/a.png", strings.NewReader("png"), 3, "image/png"); err != nil {
		t.Fatal(err)
	}
	if sawValue != "request-7" {
		t.Errorf("hook saw context value %v, want request-7", sawValue)
	}
	if storedDuringHook {
		t.Error("object was stored before the hook ran")
	}
	if err := s.Put(ctx, "groups/g/refused", strings.NewReader("x"), 1, "text/plain"); !errors.Is(err, errBoom) {
		t.Errorf("Put = %v, want the hook's error", err)
	}
	if got := s.Keys(); !slices.Equal(got, []string{"groups/g/a.png"}) {
		t.Errorf("Keys() = %q; the refused Put must store nothing", got)
	}
}

// The fakes record and return copies by deep-copying the reference-typed
// fields of what they are handed: email.Message's GroupID and Headers and
// CheckoutParams' TrialEnd. If one of these types gains another pointer,
// map, slice or similar field, or one of those fields changes type, the
// copy helpers would quietly share memory with the caller. This fails
// first, naming the field to handle in copyMessage or copyCheckout.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestFakes_CopiesCoverEveryReferenceField(t *testing.T) {
	cases := []struct {
		typ     reflect.Type
		handled []string // "Name Type" of every field holding a reference
	}{
		{reflect.TypeFor[email.Message](), []string{"GroupID *uuid.UUID", "Headers map[string]string"}},
		{reflect.TypeFor[payments.CheckoutParams](), []string{"TrialEnd *time.Time"}},
		{reflect.TypeFor[storage.Info](), nil},
		{reflect.TypeFor[net.MX](), nil},
	}
	for _, c := range cases {
		var got []string
		for f := range c.typ.Fields() {
			if holdsReference(f.Type) {
				got = append(got, f.Name+" "+f.Type.String())
			}
		}
		if !slices.Equal(got, c.handled) {
			t.Errorf("%s: reference-typed fields are %q; the fakes copy only %q", c.typ, got, c.handled)
		}
	}
}

// holdsReference reports whether a value of type t can share memory with
// a copy of it. time.Time is treated as a value: its only pointer is to an
// immutable *time.Location.
func holdsReference(t reflect.Type) bool {
	if t == reflect.TypeFor[time.Time]() {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return holdsReference(t.Elem())
	case reflect.Struct:
		for f := range t.Fields() {
			if holdsReference(f.Type) {
				return true
			}
		}
	}
	return false
}

// wait receives from ch, failing the test if nothing arrives in time.
func wait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting")
		panic("unreachable")
	}
}
