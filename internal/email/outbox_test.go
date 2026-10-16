package email_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"meet.space/internal/clock/clocktest"
	"meet.space/internal/email"
	"meet.space/internal/testkit"
	"meet.space/internal/testkit/fakes"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const (
	fromAuth   = "login@auth.example.test"
	fromNotify = "notifications@mail.example.test"
)

// The retry waits from decisions.md "Outbox retry schedule", written out
// here rather than read from the code.
var schedule = map[string][]time.Duration{
	email.StreamAuth:   {10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute},
	email.StreamNotify: {time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 4 * time.Hour, 12 * time.Hour},
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type env struct {
	db     *testkit.DB
	clk    *clocktest.Fake
	sender *fakes.Sender
	logs   *syncBuffer
	box    *email.Outbox
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{db: testkit.NewDB(t), clk: clocktest.New(t0), sender: &fakes.Sender{}, logs: &syncBuffer{}}
	e.box = e.outbox()
	return e
}

// outbox returns a poller on e's database. Tests that don't measure the
// notify rate lift the cap so they run fast.
func (e *env) outbox() *email.Outbox {
	return &email.Outbox{
		Pool:       e.db.Pool,
		Sender:     e.sender,
		Clock:      e.clk,
		Logger:     slog.New(slog.NewTextHandler(e.logs, nil)),
		FromAuth:   fromAuth,
		FromNotify: fromNotify,
		NotifyRate: 10000,
		Rand:       func() float64 { return 0.5 },
	}
}

func outgoing(stream, to, key string) email.Outgoing {
	return email.Outgoing{
		Stream:    stream,
		Kind:      "magic_link",
		To:        to,
		FromName:  "Front Range Hikers",
		Subject:   "Your login link",
		HTML:      "<p>Log in</p>",
		Text:      "Log in",
		DedupeKey: key,
	}
}

// enqueue queues m in its own committed transaction.
func (e *env) enqueue(t *testing.T, m email.Outgoing) bool {
	t.Helper()
	ok, err := enqueueTx(t.Context(), e.db.Pool, e.clk, m)
	if err != nil {
		t.Fatalf("enqueue %s: %v", m.DedupeKey, err)
	}
	return ok
}

func enqueueTx(ctx context.Context, pool *pgxpool.Pool, clk *clocktest.Fake, m email.Outgoing) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ok, err := email.Enqueue(ctx, tx, clk, m)
	if err != nil {
		return false, err
	}
	return ok, tx.Commit(ctx)
}

type outboxRow struct {
	ID          uuid.UUID
	Status      string
	Attempts    int
	NextAttempt time.Time
	LastError   *string
	SESID       *string
	SentAt      *time.Time
}

func (e *env) row(t *testing.T, key string) outboxRow {
	t.Helper()
	var r outboxRow
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT id, status, attempts, next_attempt_at, last_error, ses_message_id, sent_at
		FROM email_outbox WHERE dedupe_key = $1`, key).
		Scan(&r.ID, &r.Status, &r.Attempts, &r.NextAttempt, &r.LastError, &r.SESID, &r.SentAt); err != nil {
		t.Fatalf("row %s: %v", key, err)
	}
	return r
}

func (e *env) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM email_outbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) drain(t *testing.T) {
	t.Helper()
	if err := e.box.Drain(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

// listener returns a connection listening on the outbox channel.
func listener(t *testing.T, pool *pgxpool.Pool) *pgx.Conn {
	t.Helper()
	pc, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	conn := pc.Hijack()
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if _, err := conn.Exec(t.Context(), "LISTEN "+email.NotifyChannel); err != nil {
		t.Fatal(err)
	}
	return conn
}

// notification waits up to d for one notification on conn.
func notification(t *testing.T, conn *pgx.Conn, d time.Duration) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	n, err := conn.WaitForNotification(ctx)
	if err != nil {
		if ctx.Err() == nil {
			t.Fatalf("wait for notification: %v", err)
		}
		return "", false
	}
	return n.Payload, true
}

func errTemporary() error { return errors.New("throttled") }

// decisions.md "What Enqueue checks": every malformed email is refused
// with ErrInvalid and nothing is written, whatever the caller does with
// its transaction afterwards.
func TestEnqueue_RefusesMalformedEmail(t *testing.T) {
	e := newEnv(t)
	good := func() email.Outgoing { return outgoing(email.StreamAuth, "ann@example.com", "k") }
	cases := map[string]func(*email.Outgoing){
		"unknown stream":               func(m *email.Outgoing) { m.Stream = "marketing" },
		"empty stream":                 func(m *email.Outgoing) { m.Stream = "" },
		"stream in capitals":           func(m *email.Outgoing) { m.Stream = "AUTH" },
		"empty kind":                   func(m *email.Outgoing) { m.Kind = "" },
		"empty dedupe key":             func(m *email.Outgoing) { m.DedupeKey = "" },
		"empty recipient":              func(m *email.Outgoing) { m.To = "" },
		"recipient with display name":  func(m *email.Outgoing) { m.To = "Ann <ann@example.com>" },
		"recipient in angle brackets":  func(m *email.Outgoing) { m.To = "<ann@example.com>" },
		"two recipients":               func(m *email.Outgoing) { m.To = "ann@example.com, bob@example.com" },
		"recipient with header":        func(m *email.Outgoing) { m.To = "ann@example.com\r\nBcc: all@evil.test" },
		"recipient with newline":       func(m *email.Outgoing) { m.To = "ann@example.com\n" },
		"recipient in mixed case":      func(m *email.Outgoing) { m.To = "Ann@example.com" },
		"recipient with spaces":        func(m *email.Outgoing) { m.To = " ann@example.com" },
		"recipient with comment":       func(m *email.Outgoing) { m.To = "(hi)ann@example.com" },
		"recipient quoted local part":  func(m *email.Outgoing) { m.To = `"ann b"@example.com` },
		"recipient without domain":     func(m *email.Outgoing) { m.To = "ann@" },
		"recipient without at sign":    func(m *email.Outgoing) { m.To = "ann" },
		"recipient non-ASCII local":    func(m *email.Outgoing) { m.To = "änn@example.com" },
		"recipient non-ASCII domain":   func(m *email.Outgoing) { m.To = "ann@exämple.com" },
		"From name with CRLF":          func(m *email.Outgoing) { m.FromName = "Hikers\r\nBcc: all@evil.test" },
		"From name with LF":            func(m *email.Outgoing) { m.FromName = "Hikers\n" },
		"From name with CR":            func(m *email.Outgoing) { m.FromName = "Hikers\r" },
		"From name with tab":           func(m *email.Outgoing) { m.FromName = "Hi\tkers" },
		"From name with NUL":           func(m *email.Outgoing) { m.FromName = "Hikers\x00" },
		"From name with DEL":           func(m *email.Outgoing) { m.FromName = "Hikers\x7f" },
		"From name with C1 NEL":        func(m *email.Outgoing) { m.FromName = "Hikers\u0085" },
		"subject with CRLF":            func(m *email.Outgoing) { m.Subject = "Hi\r\nBcc: all@evil.test" },
		"subject with LF":              func(m *email.Outgoing) { m.Subject = "Hi\nthere" },
		"subject with CR":              func(m *email.Outgoing) { m.Subject = "Hi\rthere" },
		"subject with escape":          func(m *email.Outgoing) { m.Subject = "Hi\x1b[31m" },
		"header Bcc":                   func(m *email.Outgoing) { m.Headers = map[string]string{"Bcc": "all@evil.test"} },
		"header X-Mailer":              func(m *email.Outgoing) { m.Headers = map[string]string{"X-Mailer": "x"} },
		"header lowercase name":        func(m *email.Outgoing) { m.Headers = map[string]string{"list-unsubscribe": "<https://x>"} },
		"header with trailing space":   func(m *email.Outgoing) { m.Headers = map[string]string{"Reply-To ": "a@example.com"} },
		"header value with CRLF":       func(m *email.Outgoing) { m.Headers = map[string]string{"Reply-To": "a@example.com\r\nBcc: all@evil.test"} },
		"header value with LF":         func(m *email.Outgoing) { m.Headers = map[string]string{"List-Unsubscribe": "<https://x>\n"} },
		"one bad header among good":    func(m *email.Outgoing) { m.Headers = map[string]string{"Reply-To": "a@example.com", "Sender": "x"} },
		"good header, bad value later": func(m *email.Outgoing) { m.Headers = map[string]string{"List-Unsubscribe-Post": "List-Unsubscribe=One-Click\r"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := good()
			mutate(&m)
			ok, err := enqueueTx(t.Context(), e.db.Pool, e.clk, m)
			if !errors.Is(err, email.ErrInvalid) || ok {
				t.Errorf("Enqueue = %v, %v; want ErrInvalid", ok, err)
			}
			if n := e.count(t); n != 0 {
				t.Errorf("%d rows written", n)
			}
		})
	}
}

// A well-formed email is stored as given: pending, no attempts, due now by
// the injected clock, with a version 7 ID. Bodies are not checked, and
// non-ASCII text that isn't a control character is fine.
func TestEnqueue_StoresAValidEmail(t *testing.T) {
	e := newEnv(t)
	m := email.Outgoing{
		Stream:   email.StreamNotify,
		Kind:     "announcement",
		To:       "ann.o'neil+hikes@example.com",
		FromName: "Randonnée Club",
		Subject:  "Sunday's hike — moved to 9 am",
		HTML:     "<p>Line one</p>\r\n<p>Line two</p>",
		Text:     "Line one\r\nLine two\x07",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://hikers.example.test/unsubscribe/abc>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			"Reply-To":              "organizer@example.com",
		},
		DedupeKey: "announcement:1:ann",
	}
	if !e.enqueue(t, m) {
		t.Fatal("Enqueue reported a duplicate")
	}
	var (
		id                                                          uuid.UUID
		stream, kind, to, fromName, subject, html, text, key, status string
		headers                                                     map[string]string
		attempts                                                    int
		next, created                                               time.Time
	)
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT id, stream, kind, to_email, from_name, subject, body_html, body_text, headers,
		dedupe_key, status, attempts, next_attempt_at, created_at FROM email_outbox`).
		Scan(&id, &stream, &kind, &to, &fromName, &subject, &html, &text, &headers, &key, &status, &attempts, &next, &created); err != nil {
		t.Fatal(err)
	}
	if stream != m.Stream || kind != m.Kind || to != m.To || fromName != m.FromName || subject != m.Subject ||
		html != m.HTML || text != m.Text || key != m.DedupeKey || len(headers) != 3 ||
		headers["List-Unsubscribe"] != m.Headers["List-Unsubscribe"] || headers["Reply-To"] != m.Headers["Reply-To"] ||
		headers["List-Unsubscribe-Post"] != m.Headers["List-Unsubscribe-Post"] {
		t.Errorf("stored %s %s %s %q %q %q %q %v %s", stream, kind, to, fromName, subject, html, text, headers, key)
	}
	if status != "pending" || attempts != 0 || !next.Equal(t0) || !created.Equal(t0) || id.Version() != 7 {
		t.Errorf("status %s attempts %d next %v created %v id version %d", status, attempts, next, created, id.Version())
	}
}

// decisions.md "How soon a queued email is sent": the row and its
// notification exist only if the caller's transaction commits, and the
// notification arrives only after the commit.
func TestEnqueue_LivesAndNotifiesOnlyWithTheCallersCommit(t *testing.T) {
	e := newEnv(t)
	conn := listener(t, e.db.Pool)
	ctx := t.Context()

	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := email.Enqueue(ctx, tx, e.clk, outgoing(email.StreamAuth, "ann@example.com", "rolled-back")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t); n != 0 {
		t.Errorf("rollback left %d rows", n)
	}
	if p, ok := notification(t, conn, 300*time.Millisecond); ok {
		t.Errorf("rolled-back enqueue notified %q", p)
	}

	tx, err = e.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := email.Enqueue(ctx, tx, e.clk, outgoing(email.StreamNotify, "ann@example.com", "committed")); err != nil {
		t.Fatal(err)
	}
	if p, ok := notification(t, conn, 200*time.Millisecond); ok {
		t.Errorf("notified %q before commit", p)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if p, ok := notification(t, conn, 5*time.Second); !ok || p != email.StreamNotify {
		t.Errorf("after commit: notification %q, %v; want %q", p, ok, email.StreamNotify)
	}
	if n := e.count(t); n != 1 {
		t.Errorf("%d rows after commit", n)
	}
}

// development-plan.md "email_outbox": the dedupe key queues a logical
// email once. A repeat writes nothing, reports false and sends no
// notification.
func TestEnqueue_DedupeKeyQueuesOnce(t *testing.T) {
	e := newEnv(t)
	conn := listener(t, e.db.Pool)
	first := outgoing(email.StreamNotify, "ann@example.com", "rsvp_confirm:m1:ann@example.com")
	if !e.enqueue(t, first) {
		t.Fatal("first enqueue reported a duplicate")
	}
	if _, ok := notification(t, conn, 5*time.Second); !ok {
		t.Fatal("first enqueue sent no notification")
	}
	again := first
	again.Subject = "Second copy"
	if e.enqueue(t, again) {
		t.Error("repeat enqueue reported a new row")
	}
	if p, ok := notification(t, conn, 300*time.Millisecond); ok {
		t.Errorf("repeat enqueue notified %q", p)
	}
	var subject string
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT subject FROM email_outbox`).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t); n != 1 || subject != first.Subject {
		t.Errorf("%d rows, subject %q", n, subject)
	}
}

// The same dedupe key enqueued from concurrent transactions (real
// goroutines, real database) gives one row, and exactly one caller is told
// it wrote it.
func TestEnqueue_ConcurrentDuplicatesQueueOnce(t *testing.T) {
	e := newEnv(t)
	n := min(8, int(e.db.Pool.Config().MaxConns))
	var wrote atomic.Int32
	errs := testkit.Race(t, e.db.Pool, n, func(ctx context.Context, i int, tx pgx.Tx) error {
		ok, err := email.Enqueue(ctx, tx, e.clk, outgoing(email.StreamAuth, "ann@example.com", "magic_link:same"))
		if ok {
			wrote.Add(1)
		}
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("contender %d: %v", i, err)
		}
	}
	if c := e.count(t); c != 1 || wrote.Load() != 1 {
		t.Errorf("%d rows, %d callers told they wrote it; want 1 and 1", c, wrote.Load())
	}
}

// Plan Phase 6 step 10: auth rows are taken first. A login email queued
// after a notify backlog still goes out first.
func TestOutbox_AuthGoesBeforeAnOlderNotifyBacklog(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		e.enqueue(t, outgoing(email.StreamNotify, "ann@example.com", fmt.Sprint("notify-", i)))
	}
	e.clk.Advance(time.Second)
	e.enqueue(t, outgoing(email.StreamAuth, "bob@example.com", "auth"))
	e.drain(t)
	sent := e.sender.Sent()
	if len(sent) != 6 || sent[0].Stream != email.StreamAuth {
		t.Fatalf("sent %d, first on %q; want 6, auth first", len(sent), sent[0].Stream)
	}
}

// Plan Phase 6 step 10: auth rows "never wait behind a notify backlog". A
// login email queued while the backlog is being sent goes out next.
func TestOutbox_AuthDoesNotWaitBehindANotifyBacklogInProgress(t *testing.T) {
	e := newEnv(t)
	for i := range 4 {
		e.enqueue(t, outgoing(email.StreamNotify, "ann@example.com", fmt.Sprint("notify-", i)))
	}
	var hookErr error
	e.sender.OnCall(func(ctx context.Context, c fakes.SenderCall) error {
		if c.N == 1 {
			_, hookErr = enqueueTx(ctx, e.db.Pool, e.clk, outgoing(email.StreamAuth, "bob@example.com", "auth"))
		}
		return nil
	})
	e.drain(t)
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	var order []string
	for _, m := range e.sender.Sent() {
		order = append(order, m.Stream)
	}
	if want := "notify auth notify notify notify"; strings.Join(order, " ") != want {
		t.Errorf("send order %v, want %s", order, want)
	}
}

// Plan Phase 6 step 10: notify sends are capped at a rate (5 per second by
// default); auth sends are not.
func TestOutbox_CapsTheNotifyRateButNotAuth(t *testing.T) {
	t.Run("notify", func(t *testing.T) {
		e := newEnv(t)
		e.box.NotifyRate = 20 // one every 50 ms
		for i := range 10 {
			e.enqueue(t, outgoing(email.StreamNotify, "ann@example.com", fmt.Sprint("n-", i)))
		}
		var mu sync.Mutex
		var at []time.Time
		e.sender.OnCall(func(context.Context, fakes.SenderCall) error {
			mu.Lock()
			defer mu.Unlock()
			at = append(at, time.Now())
			return nil
		})
		e.drain(t)
		if len(at) != 10 {
			t.Fatalf("%d sends", len(at))
		}
		// A ticker may deliver one buffered tick early, so allow one short gap.
		if got, min := at[9].Sub(at[0]), 8*50*time.Millisecond*9/10; got < min {
			t.Errorf("10 notify sends took %v; at 20/s they need at least %v", got, min)
		}
	})
	t.Run("auth", func(t *testing.T) {
		e := newEnv(t)
		e.box.NotifyRate = 1
		for i := range 6 {
			e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", fmt.Sprint("a-", i)))
		}
		var mu sync.Mutex
		var at []time.Time
		e.sender.OnCall(func(context.Context, fakes.SenderCall) error {
			mu.Lock()
			defer mu.Unlock()
			at = append(at, time.Now())
			return nil
		})
		e.drain(t)
		if len(at) != 6 {
			t.Fatalf("%d sends", len(at))
		}
		if got := at[5].Sub(at[0]); got > 500*time.Millisecond {
			t.Errorf("6 auth sends took %v with the notify rate at 1/s", got)
		}
	})
}

// Plan Phase 6 step 10: a row is marked sending before the provider is
// called. During the call it is claimed: one attempt counted, and due
// again only after the 2-minute claim (decisions.md "Outbox rows stuck in
// sending"). The provider gets the stored email with its stream's From
// address; a success records the message ID and sent time.
func TestOutbox_ClaimsBeforeSendingAndRecordsTheSend(t *testing.T) {
	for _, stream := range []string{email.StreamAuth, email.StreamNotify} {
		t.Run(stream, func(t *testing.T) {
			e := newEnv(t)
			m := outgoing(stream, "ann@example.com", "k")
			m.Headers = map[string]string{"Reply-To": "organizer@example.com"}
			e.enqueue(t, m)
			var during outboxRow
			e.sender.OnCall(func(ctx context.Context, c fakes.SenderCall) error {
				during = e.row(t, "k")
				return nil
			})
			e.clk.Advance(time.Second)
			e.drain(t)
			if during.Status != "sending" || during.Attempts != 1 || !during.NextAttempt.Equal(t0.Add(time.Second+2*time.Minute)) {
				t.Errorf("during the send: %s, attempt %d, next %v", during.Status, during.Attempts, during.NextAttempt)
			}
			sent := e.sender.Sent()
			if len(sent) != 1 {
				t.Fatalf("%d sends", len(sent))
			}
			want := map[string]string{email.StreamAuth: fromAuth, email.StreamNotify: fromNotify}[stream]
			got := sent[0]
			if got.FromAddress != want || got.OutboxID != during.ID || got.Stream != stream || got.Kind != m.Kind || got.To != m.To ||
				got.FromName != m.FromName || got.Subject != m.Subject || got.HTML != m.HTML || got.Text != m.Text ||
				got.Headers["Reply-To"] != "organizer@example.com" || len(got.Headers) != 1 || got.GroupID != nil {
				t.Errorf("sent %+v", got)
			}
			after := e.row(t, "k")
			if after.Status != "sent" || after.SESID == nil || *after.SESID != "fake-1" || after.SentAt == nil || !after.SentAt.Equal(t0.Add(time.Second)) {
				t.Errorf("after: %+v", after)
			}
		})
	}
}

// decisions.md "Outbox rows stuck in sending": each provider call has a
// 30-second deadline, even when the poller's own context has none.
func TestOutbox_SendHasA30SecondDeadline(t *testing.T) {
	e := newEnv(t)
	e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", "k"))
	var left time.Duration
	var has bool
	e.sender.OnCall(func(ctx context.Context, c fakes.SenderCall) error {
		var d time.Time
		d, has = ctx.Deadline()
		left = time.Until(d)
		return nil
	})
	if err := e.box.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !has || left > 30*time.Second || left < 29*time.Second {
		t.Errorf("deadline %v, %v from now; want 30s", has, left)
	}
}

// decisions.md "Outbox retry schedule": after each temporary failure the
// row waits the stream's next wait (here with the variation at its
// midpoint), is not taken a moment early, and is dead after the last try.
// Nothing more is sent.
func TestOutbox_RetriesOnTheStreamsScheduleThenGivesUp(t *testing.T) {
	for stream, waits := range schedule {
		t.Run(stream, func(t *testing.T) {
			e := newEnv(t)
			e.sender.FailWhen(func(fakes.SenderCall) bool { return true }, errTemporary())
			e.enqueue(t, outgoing(stream, "ann@example.com", "k"))
			for try := 1; try <= len(waits)+1; try++ {
				e.drain(t)
				r := e.row(t, "k")
				if got := len(e.sender.Calls()); got != try || r.Attempts != try {
					t.Fatalf("try %d: %d calls, %d attempts", try, got, r.Attempts)
				}
				if r.LastError == nil || *r.LastError != "throttled" {
					t.Errorf("try %d: last_error %v", try, r.LastError)
				}
				if try == len(waits)+1 {
					if r.Status != "dead" {
						t.Errorf("after the last try: %s, want dead", r.Status)
					}
					break
				}
				if want := e.clk.Now().Add(waits[try-1]); r.Status != "failed" || !r.NextAttempt.Equal(want) {
					t.Fatalf("try %d: %s, next %v; want failed, %v", try, r.Status, r.NextAttempt, want)
				}
				e.clk.Set(r.NextAttempt.Add(-time.Microsecond))
				e.drain(t)
				if got := len(e.sender.Calls()); got != try {
					t.Fatalf("try %d: retried before its wait was up", try)
				}
				e.clk.Set(r.NextAttempt)
			}
			e.clk.Advance(48 * time.Hour)
			e.drain(t)
			if got := len(e.sender.Calls()); got != len(waits)+1 {
				t.Errorf("%d calls after giving up, want %d", got, len(waits)+1)
			}
		})
	}
}

// decisions.md "Outbox retry schedule": each wait varies by ±20%.
func TestOutbox_RetryWaitVariesByUpTo20Percent(t *testing.T) {
	cases := []struct {
		name     string
		rand     func() float64
		low, top float64 // bounds as a fraction of the wait
	}{
		{"lowest", func() float64 { return 0 }, 0.8, 0.8},
		{"highest", func() float64 { return 0.9999999 }, 1.199, 1.2},
		{"random", nil, 0.8, 1.2},
	}
	for _, c := range cases {
		for stream, waits := range schedule {
			t.Run(c.name+"/"+stream, func(t *testing.T) {
				e := newEnv(t)
				e.box.Rand = c.rand
				e.sender.FailWhen(func(fakes.SenderCall) bool { return true }, errTemporary())
				e.enqueue(t, outgoing(stream, "ann@example.com", "k"))
				e.drain(t)
				got := e.row(t, "k").NextAttempt.Sub(e.clk.Now())
				w := float64(waits[0])
				if float64(got) < c.low*w-float64(time.Microsecond) || float64(got) > c.top*w {
					t.Errorf("wait %v, want between %v and %v", got, time.Duration(c.low*w), time.Duration(c.top*w))
				}
			})
		}
	}
}

// decisions.md "Outbox permanent send errors": a permanent error ends the
// email at once.
func TestOutbox_PermanentErrorGivesUpAtOnce(t *testing.T) {
	e := newEnv(t)
	e.sender.FailWhen(func(fakes.SenderCall) bool { return true }, email.Permanent(errors.New("address rejected")))
	e.enqueue(t, outgoing(email.StreamNotify, "ann@example.com", "k"))
	e.drain(t)
	r := e.row(t, "k")
	if r.Status != "dead" || r.Attempts != 1 || r.LastError == nil || *r.LastError != "address rejected" {
		t.Errorf("after a permanent error: %+v", r)
	}
	e.clk.Advance(48 * time.Hour)
	e.drain(t)
	if n := len(e.sender.Calls()); n != 1 {
		t.Errorf("%d calls, want 1", n)
	}
}

// decisions.md "Outbox permanent send errors": an account-wide error is
// temporary (the row retries on schedule) and is logged as an error; any
// other temporary error is logged as a warning.
func TestOutbox_AccountWideErrorIsRetriedAndLoggedAsError(t *testing.T) {
	e := newEnv(t)
	e.sender.FailWhen(func(c fakes.SenderCall) bool { return c.Message.To == "ann@example.com" },
		email.AccountWide(errors.New("sending paused")))
	e.sender.FailWhen(func(c fakes.SenderCall) bool { return c.Message.To == "bob@example.com" }, errTemporary())
	e.enqueue(t, outgoing(email.StreamNotify, "ann@example.com", "account"))
	e.enqueue(t, outgoing(email.StreamNotify, "bob@example.com", "plain"))
	e.drain(t)
	for _, key := range []string{"account", "plain"} {
		r := e.row(t, key)
		if r.Status != "failed" || r.Attempts != 1 || !r.NextAttempt.Equal(t0.Add(time.Minute)) {
			t.Errorf("%s: %+v", key, r)
		}
	}
	var accountLine, plainLine string
	for l := range strings.Lines(e.logs.String()) {
		switch {
		case strings.Contains(l, "sending paused"):
			accountLine = l
		case strings.Contains(l, "throttled"):
			plainLine = l
		}
	}
	if !strings.Contains(accountLine, "level=ERROR") {
		t.Errorf("account-wide failure logged as %q", accountLine)
	}
	if !strings.Contains(plainLine, "level=WARN") {
		t.Errorf("temporary failure logged as %q", plainLine)
	}
}

// decisions.md "Outbox rows stuck in sending": a row whose poller died
// mid-send is not touched until its 2-minute claim runs out, then is sent,
// with the lost try counted.
func TestOutbox_ExpiredClaimIsTakenAgainAfterTwoMinutes(t *testing.T) {
	e := newEnv(t)
	e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", "k"))
	if _, ok, err := email.Claim(t.Context(), e.box, email.StreamAuth); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	e.drain(t)
	e.clk.Advance(2*time.Minute - time.Microsecond)
	e.drain(t)
	if n := len(e.sender.Calls()); n != 0 {
		t.Fatalf("claimed row sent %d times before its claim ran out", n)
	}
	e.clk.Advance(time.Microsecond)
	e.drain(t)
	r := e.row(t, "k")
	if n := len(e.sender.Calls()); n != 1 || r.Status != "sent" || r.Attempts != 2 {
		t.Errorf("after the claim ran out: %d sends, %s, attempt %d", n, r.Status, r.Attempts)
	}
}

// decisions.md "Outbox rows stuck in sending": a row whose claims keep
// running out uses up its tries and becomes dead without another send.
func TestOutbox_ExpiredClaimWithNoTriesLeftIsDead(t *testing.T) {
	for stream, waits := range schedule {
		t.Run(stream, func(t *testing.T) {
			e := newEnv(t)
			e.enqueue(t, outgoing(stream, "ann@example.com", "k"))
			for range len(waits) + 1 {
				if _, ok, err := email.Claim(t.Context(), e.box, stream); err != nil || !ok {
					t.Fatalf("claim: %v %v", ok, err)
				}
				e.clk.Advance(2 * time.Minute)
			}
			e.drain(t)
			r := e.row(t, "k")
			if r.Status != "dead" || r.Attempts != len(waits)+1 || r.LastError == nil || *r.LastError != "claim expired" {
				t.Errorf("%+v", r)
			}
			if n := len(e.sender.Calls()); n != 0 {
				t.Errorf("%d sends", n)
			}
		})
	}
}

// decisions.md "Outbox rows stuck in sending": finishing a send is fenced
// on the claim, so a poller that finishes after its claim ran out and the
// row was taken again changes nothing, whatever its outcome.
func TestOutbox_LateCompletionAfterAReclaimChangesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", "k"))
	first, _, err := email.Claim(ctx, e.box, email.StreamAuth)
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(2 * time.Minute)
	second, _, err := email.Claim(ctx, e.box, email.StreamAuth)
	if err != nil || second == nil {
		t.Fatalf("reclaim: %v", err)
	}
	late := []error{nil, errTemporary(), email.Permanent(errors.New("rejected"))}
	for _, outcome := range late {
		if err := email.Complete(ctx, e.box, first, "late-id", outcome); err != nil {
			t.Fatal(err)
		}
		if r := e.row(t, "k"); r.Status != "sending" || r.Attempts != 2 || r.SESID != nil {
			t.Errorf("late completion (%v) changed the row: %+v", outcome, r)
		}
	}
	if err := email.Complete(ctx, e.box, second, "current-id", nil); err != nil {
		t.Fatal(err)
	}
	for _, outcome := range late {
		if err := email.Complete(ctx, e.box, first, "late-id", outcome); err != nil {
			t.Fatal(err)
		}
	}
	if r := e.row(t, "k"); r.Status != "sent" || r.SESID == nil || *r.SESID != "current-id" {
		t.Errorf("after the current claim finished: %+v", r)
	}
}

// decisions.md "Outbox rows stuck in sending": a send that finishes after
// its row was given up (its claim ran out with no tries left) leaves the
// row dead.
func TestOutbox_LateCompletionAfterTheRowWasGivenUpChangesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", "k"))
	var last email.Claimed
	for range len(schedule[email.StreamAuth]) + 1 {
		c, ok, err := email.Claim(ctx, e.box, email.StreamAuth)
		if err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		last = c
		e.clk.Advance(2 * time.Minute)
	}
	e.drain(t)
	if r := e.row(t, "k"); r.Status != "dead" {
		t.Fatalf("row %s, want dead", r.Status)
	}
	if err := email.Complete(ctx, e.box, last, "late-id", nil); err != nil {
		t.Fatal(err)
	}
	if r := e.row(t, "k"); r.Status != "dead" || r.SESID != nil || r.SentAt != nil {
		t.Errorf("late success changed a dead row: %+v", r)
	}
}

// A row another transaction holds locked is skipped, not waited on: the
// poller sends the other due rows and returns (FOR UPDATE SKIP LOCKED).
func TestOutbox_SkipsARowLockedElsewhere(t *testing.T) {
	e := newEnv(t)
	e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", "locked"))
	e.clk.Advance(time.Second)
	e.enqueue(t, outgoing(email.StreamAuth, "bob@example.com", "free"))
	tx, err := e.db.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SELECT id FROM email_outbox WHERE dedupe_key = 'locked' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := e.box.Drain(ctx); err != nil {
		t.Fatalf("drain with a row locked elsewhere: %v", err)
	}
	sent := e.sender.Sent()
	if len(sent) != 1 || sent[0].To != "bob@example.com" {
		t.Errorf("sent %d emails; want only the unlocked one", len(sent))
	}
}

// Two pollers draining one queue at once (real goroutines, real database)
// send every email exactly once.
func TestOutbox_TwoPollersSendEachEmailOnce(t *testing.T) {
	e := newEnv(t)
	const n = 120
	for i := range n {
		stream := email.StreamNotify
		if i%3 == 0 {
			stream = email.StreamAuth
		}
		e.enqueue(t, outgoing(stream, "ann@example.com", fmt.Sprint("k-", i)))
	}
	pollers := []*email.Outbox{e.box, e.outbox()}
	var wg sync.WaitGroup
	errs := make([]error, len(pollers))
	for i, p := range pollers {
		wg.Go(func() { errs[i] = p.Drain(t.Context()) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("poller %d: %v", i, err)
		}
	}
	seen := map[uuid.UUID]int{}
	for _, c := range e.sender.Calls() {
		seen[c.Message.OutboxID]++
	}
	for id, k := range seen {
		if k != 1 {
			t.Errorf("email %s sent %d times", id, k)
		}
	}
	var sent int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM email_outbox WHERE status = 'sent'`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if len(seen) != n || sent != n {
		t.Errorf("%d distinct emails sent, %d rows sent; want %d", len(seen), sent, n)
	}
}

// run starts the poller and returns a channel that receives each time it
// has drained and is waiting, and a stop function that waits for Run to
// return.
func (e *env) run(t *testing.T) (waiting <-chan struct{}, stop func()) {
	t.Helper()
	w := make(chan struct{}, 100)
	email.SetOnWait(e.box, func() {
		select {
		case w <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.box.Run(ctx)
	}()
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Run did not return after its context was canceled")
		}
	}
	t.Cleanup(stop)
	select {
	case <-w:
	case <-time.After(10 * time.Second):
		t.Fatal("poller never reached its first wait")
	}
	return w, stop
}

func (e *env) sentWithin(d time.Duration, n int) bool {
	deadline := time.After(d)
	for {
		if len(e.sender.Sent()) >= n {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func insertWithoutNotify(t *testing.T, e *env, key string) {
	t.Helper()
	if _, err := e.db.Pool.Exec(t.Context(), `INSERT INTO email_outbox
		(id, stream, kind, to_email, from_name, subject, body_html, body_text, dedupe_key, next_attempt_at)
		VALUES (gen_random_uuid(), 'auth', 'magic_link', 'ann@example.com', 'x', 'x', 'x', 'x', $1, $2)`, key, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
}

// decisions.md "How soon a queued email is sent": a committed enqueue
// wakes the poller at once. With the poll an hour away, a row inserted
// without a notification waits, and the next enqueue's notification sends
// both.
func TestOutbox_NotificationWakesThePoller(t *testing.T) {
	e := newEnv(t)
	e.box.PollInterval = time.Hour
	e.run(t)
	insertWithoutNotify(t, e, "silent")
	if e.sentWithin(300*time.Millisecond, 1) {
		t.Fatal("a row with no notification was sent before the poll")
	}
	e.enqueue(t, outgoing(email.StreamAuth, "bob@example.com", "loud"))
	if !e.sentWithin(3*time.Second, 2) {
		t.Errorf("sent %d of 2 within 3s of the notification", len(e.sender.Sent()))
	}
}

// decisions.md "How soon a queued email is sent": the poll finds due rows
// that no notification announced.
func TestOutbox_PollSendsRowsThatHadNoNotification(t *testing.T) {
	e := newEnv(t)
	e.box.PollInterval = 100 * time.Millisecond
	e.run(t)
	insertWithoutNotify(t, e, "silent")
	if !e.sentWithin(3*time.Second, 1) {
		t.Error("the poll never sent the row")
	}
}

// Stopping the poller lets a send in progress finish and be recorded.
func TestOutbox_StopFinishesTheSendInProgress(t *testing.T) {
	e := newEnv(t)
	entered, release := make(chan struct{}), make(chan struct{})
	e.sender.OnCall(func(ctx context.Context, c fakes.SenderCall) error {
		close(entered)
		<-release
		return ctx.Err()
	})
	_, stop := e.run(t)
	e.enqueue(t, outgoing(email.StreamAuth, "ann@example.com", "k"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("send never started")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Run returned while a send was in progress")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-stopped
	if r := e.row(t, "k"); r.Status != "sent" {
		t.Errorf("row %s after stopping", r.Status)
	}
}

// Plan Phase 0 step 10: the dev sender writes each email to its output so
// a magic link can be read there, and returns an ID.
func TestLogSender_WritesTheEmail(t *testing.T) {
	var out bytes.Buffer
	s := &email.LogSender{W: &out}
	id := uuid.Must(uuid.NewV7())
	got, err := s.Send(t.Context(), email.Message{
		OutboxID: id, Stream: email.StreamAuth, Kind: "magic_link", To: "ann@example.com",
		FromName: "Front Range Hikers", FromAddress: fromAuth, Subject: "Your login link",
		HTML: "<p>html</p>", Text: "Log in: http://hikers.localhost:8080/auth/verify/abc",
	})
	if err != nil || got != "dev-"+id.String() {
		t.Errorf("Send = %q, %v", got, err)
	}
	for _, want := range []string{"To: ann@example.com", "From: Front Range Hikers <" + fromAuth + ">",
		"Subject: Your login link", "Log in: http://hikers.localhost:8080/auth/verify/abc", id.String()} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
