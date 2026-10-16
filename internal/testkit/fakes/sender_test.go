package fakes_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"

	"meet.space/internal/email"
	"meet.space/internal/testkit"
	"meet.space/internal/testkit/fakes"
)

func message(to, stream string) email.Message {
	g := uuid.MustParse("0192f1d2-0000-7000-8000-000000000001")
	return email.Message{
		OutboxID:    uuid.MustParse("0192f1d2-0000-7000-8000-0000000000aa"),
		Stream:      stream,
		Kind:        "rsvp_confirmation",
		GroupID:     &g,
		To:          to,
		FromName:    "Hikers",
		FromAddress: "notifications@mail.meet.space",
		Subject:     "You're going",
		HTML:        "<p>See you there</p>",
		Text:        "See you there",
		Headers:     map[string]string{"List-Unsubscribe-Post": "List-Unsubscribe=One-Click"},
	}
}

// The Sender records whole messages, in the order Send was called, and by
// default returns fake-1, fake-2, ... for successful sends.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestSender_RecordsWholeMessagesInOrder(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Sender{}
	msgs := []email.Message{
		message("ann@example.com", email.StreamAuth),
		message("bob@example.com", email.StreamNotify),
		message("cat@example.com", email.StreamNotify),
	}
	for i, m := range msgs {
		id, err := s.Send(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"fake-1", "fake-2", "fake-3"}[i]; id != want {
			t.Errorf("send %d returned ID %q, want %q", i+1, id, want)
		}
	}
	if got := s.Sent(); !reflect.DeepEqual(got, msgs) {
		t.Errorf("Sent() =\n%+v\nwant\n%+v", got, msgs)
	}
	for i, c := range s.Calls() {
		if c.N != i+1 || c.Method != "Send" || !c.Done || c.Err != nil || !reflect.DeepEqual(c.Message, msgs[i]) {
			t.Errorf("call %d = %+v", i+1, c)
		}
	}
}

// What the Sender recorded can't be changed by the caller afterwards,
// and what it returns can't change it.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestSender_RecordsAndReturnsCopies(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Sender{}
	m := message("ann@example.com", email.StreamNotify)
	want := message("ann@example.com", email.StreamNotify)
	if _, err := s.Send(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.Headers["List-Unsubscribe"] = "<https://evil.example>"
	*m.GroupID = uuid.Nil

	got := s.Sent()
	got[0].Headers["X-Changed"] = "1"
	*got[0].GroupID = uuid.Nil
	got[0].Subject = "changed"
	calls := s.Calls()
	calls[0].Message.Headers["X-Changed"] = "1"

	if again := s.Sent(); !reflect.DeepEqual(again[0], want) {
		t.Errorf("recorded message changed:\n got %+v\nwant %+v", again[0], want)
	}
}

// A failed send returns the error and no ID, is recorded with its error,
// and is not in Sent(). Message IDs count successful sends.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestSender_FailNthAndFailWhen(t *testing.T) {
	ctx := context.Background()
	errThrottled := errors.New("throttled")
	s := &fakes.Sender{}
	s.FailNth(2, errBoom)
	s.FailWhen(func(c fakes.SenderCall) bool { return c.Message.To == "suppressed@example.com" }, errThrottled)

	type sendResult struct {
		id  string
		err error
	}
	var got []sendResult
	for _, to := range []string{"ann@example.com", "bob@example.com", "suppressed@example.com", "cat@example.com"} {
		id, err := s.Send(ctx, message(to, email.StreamNotify))
		got = append(got, sendResult{id, err})
	}
	want := []sendResult{{"fake-1", nil}, {"", errBoom}, {"", errThrottled}, {"fake-2", nil}}
	for i := range want {
		if got[i].id != want[i].id || !errors.Is(got[i].err, want[i].err) || (want[i].err == nil && got[i].err != nil) {
			t.Errorf("send %d = %q, %v; want %q, %v", i+1, got[i].id, got[i].err, want[i].id, want[i].err)
		}
	}
	var sentTo []string
	for _, m := range s.Sent() {
		sentTo = append(sentTo, m.To)
	}
	if want := []string{"ann@example.com", "cat@example.com"}; !slices.Equal(sentTo, want) {
		t.Errorf("Sent() to %q, want %q", sentTo, want)
	}
	calls := s.Calls()
	if len(calls) != 4 || !errors.Is(calls[1].Err, errBoom) || !errors.Is(calls[2].Err, errThrottled) || calls[1].MessageID != "" {
		t.Errorf("calls = %+v", calls)
	}
}

// SetMessageIDs chooses the IDs successful sends return.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestSender_SetMessageIDsChoosesIDs(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Sender{}
	s.SetMessageIDs(func(k int, m email.Message) string { return m.Stream + "-" + string(rune('a'+k-1)) })
	id1, _ := s.Send(ctx, message("ann@example.com", email.StreamAuth))
	id2, _ := s.Send(ctx, message("bob@example.com", email.StreamNotify))
	if id1 != "auth-a" || id2 != "notify-b" {
		t.Errorf("IDs = %q, %q; want auth-a, notify-b", id1, id2)
	}
	if c := s.Calls(); c[0].MessageID != "auth-a" || c[1].MessageID != "notify-b" {
		t.Errorf("recorded IDs = %q, %q", c[0].MessageID, c[1].MessageID)
	}
}

// A hook can read the test's database while the send is in progress, for
// example to check what the outbox row looked like at the moment of
// sending.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestSender_HookCanQueryTheDatabaseMidSend(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email) VALUES (gen_random_uuid(), 'ann@example.com')`); err != nil {
		t.Fatal(err)
	}
	s := &fakes.Sender{}
	var users int
	s.OnCall(func(ctx context.Context, c fakes.SenderCall) error {
		return db.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, c.Message.To).Scan(&users)
	})
	if _, err := s.Send(ctx, message("ann@example.com", email.StreamAuth)); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Errorf("hook counted %d users with the recipient's email, want 1", users)
	}
}
