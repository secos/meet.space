package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"meet.space/internal/clock"
)

// NotifyChannel is the Postgres channel Enqueue notifies and the poller
// listens on (decisions.md "How soon a queued email is sent").
const NotifyChannel = "email_outbox"

// Timings (decisions.md "Outbox rows stuck in sending", "How soon a queued
// email is sent").
const (
	// ClaimDuration is how long a row taken for sending stays claimed. A
	// row still 'sending' after that is due again.
	ClaimDuration = 2 * time.Minute
	// SendTimeout bounds each call to the Sender, so a live send never
	// outlasts its claim.
	SendTimeout = 30 * time.Second
	// DefaultPollInterval is how often the poller looks for due rows when
	// no notification wakes it.
	DefaultPollInterval = 5 * time.Second
	// DefaultNotifyRate caps notify sends per second (development-plan.md
	// Phase 6 step 10).
	DefaultNotifyRate = 5
)

// retryWaits are the waits before each retry, per stream (decisions.md
// "Outbox retry schedule"). An email gets one try plus one per wait; each
// wait varies by ±20%.
var retryWaits = map[string][]time.Duration{
	StreamAuth:   {10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute},
	StreamNotify: {time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 4 * time.Hour, 12 * time.Hour},
}

// maxAttempts is how many tries an email on stream gets in all.
func maxAttempts(stream string) int { return len(retryWaits[stream]) + 1 }

// Headers an email may carry besides the ones the provider sets
// (decisions.md "What Enqueue checks").
var allowedHeaders = map[string]bool{
	"List-Unsubscribe":      true,
	"List-Unsubscribe-Post": true,
	"Reply-To":              true,
}

// ErrInvalid is returned, wrapped, by Enqueue for an email it refuses.
var ErrInvalid = errors.New("email: invalid outgoing email")

// Outgoing is an email to queue. The From address is not part of it: the
// poller adds the address configured for the stream.
type Outgoing struct {
	Stream    string
	Kind      string
	GroupID   *uuid.UUID
	To        string
	FromName  string
	Subject   string
	HTML      string
	Text      string
	Headers   map[string]string
	DedupeKey string
}

// DBTX is what Enqueue needs from the caller's transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Enqueue queues m inside the caller's transaction tx, so the email exists
// only if the caller's change commits. It reports whether a row was
// written: false means an email with the same dedupe key was already
// queued. A new row also notifies NotifyChannel, which Postgres delivers
// only on commit. An email that fails the checks in validate is refused
// with an error wrapping ErrInvalid, and nothing is written.
func Enqueue(ctx context.Context, tx DBTX, clk clock.Clock, m Outgoing) (bool, error) {
	if err := validate(m); err != nil {
		return false, err
	}
	headers := m.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	hj, err := json.Marshal(headers)
	if err != nil {
		return false, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	now := clk.Now()
	tag, err := tx.Exec(ctx, `INSERT INTO email_outbox
		(id, stream, kind, group_id, to_email, from_name, subject, body_html, body_text, headers, dedupe_key, next_attempt_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		id, m.Stream, m.Kind, m.GroupID, m.To, m.FromName, m.Subject, m.HTML, m.Text, hj, m.DedupeKey, now)
	if err != nil {
		return false, fmt.Errorf("enqueue %s: %w", m.Kind, err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, m.Stream); err != nil {
		return false, fmt.Errorf("enqueue %s: notify: %w", m.Kind, err)
	}
	return true, nil
}

// validate applies decisions.md "What Enqueue checks". A header value
// carrying a line break could add headers of its own, so the From name,
// subject and header values may hold no control character at all.
func validate(m Outgoing) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if m.Stream != StreamAuth && m.Stream != StreamNotify {
		return bad("stream %q", m.Stream)
	}
	if m.Kind == "" {
		return bad("empty kind")
	}
	if m.DedupeKey == "" {
		return bad("empty dedupe key")
	}
	if !plainAddress(m.To) {
		return bad("recipient %q is not one plain lowercase address", m.To)
	}
	if hasControl(m.FromName) {
		return bad("From name contains a control character")
	}
	if hasControl(m.Subject) {
		return bad("subject contains a control character")
	}
	for k, v := range m.Headers {
		if !allowedHeaders[k] {
			return bad("header %q is not allowed", k)
		}
		if hasControl(v) {
			return bad("header %s contains a control character", k)
		}
	}
	return nil
}

// plainAddress reports whether s is one address with no display name,
// comment or angle brackets, in lowercase printable ASCII. Non-ASCII
// addresses wait for their own owner question.
func plainAddress(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	if s != strings.ToLower(s) {
		return false
	}
	// A display name, comment or brackets would make s differ from the
	// parsed address.
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// Outbox is the poller that sends queued email. Run it once per process;
// several pollers may run against one database, since rows are claimed
// with FOR UPDATE SKIP LOCKED.
type Outbox struct {
	Pool   *pgxpool.Pool
	Sender Sender
	Clock  clock.Clock
	Logger *slog.Logger
	// FromAuth and FromNotify are the From addresses for each stream.
	FromAuth, FromNotify string
	// PollInterval is how often to look for due rows without a
	// notification; zero means DefaultPollInterval.
	PollInterval time.Duration
	// NotifyRate caps notify sends per second; zero means
	// DefaultNotifyRate. Auth sends are not capped.
	NotifyRate float64
	// Rand returns a number in [0, 1) for the retry jitter; nil means
	// math/rand/v2.
	Rand func() float64

	tickOnce   sync.Once
	notifyTick *time.Ticker
	// onWait, when set (by tests), runs each time Run has drained and is
	// about to wait for a notification or the next poll.
	onWait func()
}

// Run listens for new email and sends what is due until ctx is done, then
// returns once any send in progress has finished. A lost listening
// connection is logged and re-opened; the poll keeps running meanwhile.
func (o *Outbox) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := o.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		o.logger().Error("outbox: listener stopped; polling until it is back", "err", err)
		o.drainLogged(ctx)
		o.wait(ctx, nil)
	}
}

func (o *Outbox) listen(ctx context.Context) error {
	pc, err := o.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The listening connection never goes back to the pool.
	conn := pc.Hijack()
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return err
	}
	for {
		o.drainLogged(ctx)
		if err := o.wait(ctx, conn); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// wait returns when a notification arrives on conn, the poll interval
// passes, or ctx is done. Only a broken connection is an error.
func (o *Outbox) wait(ctx context.Context, conn *pgx.Conn) error {
	if o.onWait != nil {
		o.onWait()
	}
	wctx, cancel := context.WithTimeout(ctx, o.pollInterval())
	defer cancel()
	if conn == nil {
		<-wctx.Done()
		return nil
	}
	_, err := conn.WaitForNotification(wctx)
	if err != nil && wctx.Err() == nil {
		return err
	}
	return nil
}

func (o *Outbox) drainLogged(ctx context.Context) {
	if err := o.Drain(ctx); err != nil && ctx.Err() == nil {
		o.logger().Error("outbox: drain failed", "err", err)
	}
}

// Drain sends every email that is due now and returns when none is left.
// Auth email always goes first: the auth stream is checked before every
// notify send, so a login link never waits behind a notify backlog
// (development-plan.md Phase 6 step 10). Notify sends are capped at
// NotifyRate.
func (o *Outbox) Drain(ctx context.Context) error {
	for ctx.Err() == nil {
		did, err := o.processNext(ctx, StreamAuth)
		if err != nil {
			return err
		}
		if did {
			continue
		}
		if err := o.awaitNotifySlot(ctx); err != nil {
			return err
		}
		did, err = o.processNext(ctx, StreamNotify)
		if err != nil || !did {
			return err
		}
	}
	return ctx.Err()
}

// awaitNotifySlot waits for the next tick of the notify rate limiter.
func (o *Outbox) awaitNotifySlot(ctx context.Context) error {
	o.tickOnce.Do(func() {
		rate := o.NotifyRate
		if rate <= 0 {
			rate = DefaultNotifyRate
		}
		o.notifyTick = time.NewTicker(time.Duration(float64(time.Second) / rate))
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-o.notifyTick.C:
		return nil
	}
}

// claim is a row taken for sending.
type claim struct {
	msg      Message
	attempts int // the attempt this claim is, counted from 1
}

// processNext claims the next due row on stream and sends it. It reports
// whether there was a row to process.
func (o *Outbox) processNext(ctx context.Context, stream string) (bool, error) {
	c, ok, err := o.claim(ctx, stream)
	if err != nil || !ok {
		return false, err
	}
	if c == nil {
		return true, nil // an expired claim with no tries left, now dead
	}
	// A send in progress finishes even when ctx is canceled at shutdown.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SendTimeout)
	id, sendErr := o.Sender.Send(sctx, c.msg)
	cancel()
	return true, o.complete(context.WithoutCancel(ctx), c, id, sendErr)
}

// claim takes the oldest due row on stream (decisions.md "Outbox rows
// stuck in sending"). Due means pending, failed, or sending with an
// expired claim, and next_attempt_at reached. Taking it counts an attempt
// and claims it for ClaimDuration. A row whose claim expired with no tries
// left becomes dead instead; then c is nil and ok is true.
func (o *Outbox) claim(ctx context.Context, stream string) (c *claim, ok bool, err error) {
	tx, err := o.Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	now := o.Clock.Now()
	var (
		m        Message
		status   string
		attempts int
		headers  map[string]string
	)
	err = tx.QueryRow(ctx, `SELECT id, status, attempts, kind, group_id, to_email, from_name, subject, body_html, body_text, headers
		FROM email_outbox
		WHERE stream = $1 AND status IN ('pending', 'failed', 'sending') AND next_attempt_at <= $2
		ORDER BY next_attempt_at, id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, stream, now).
		Scan(&m.OutboxID, &status, &attempts, &m.Kind, &m.GroupID, &m.To, &m.FromName, &m.Subject, &m.HTML, &m.Text, &headers)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if status == "sending" && attempts >= maxAttempts(stream) {
		if _, err := tx.Exec(ctx, `UPDATE email_outbox SET status = 'dead', last_error = 'claim expired' WHERE id = $1`, m.OutboxID); err != nil {
			return nil, false, err
		}
		o.logger().Warn("outbox: claim expired with no tries left; giving up", "id", m.OutboxID, "stream", stream, "kind", m.Kind, "attempts", attempts)
		return nil, true, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE email_outbox
		SET status = 'sending', attempts = attempts + 1, next_attempt_at = $2,
		    last_error = CASE WHEN status = 'sending' THEN 'claim expired' ELSE last_error END
		WHERE id = $1`, m.OutboxID, now.Add(ClaimDuration)); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	m.Stream = stream
	m.Headers = headers
	m.FromAddress = o.FromNotify
	if stream == StreamAuth {
		m.FromAddress = o.FromAuth
	}
	return &claim{msg: m, attempts: attempts + 1}, true, nil
}

// complete records how a send went. Every update is fenced on the claim:
// it applies only while the row is still 'sending' at this claim's attempt
// count, so a send that finishes after its claim expired and the row was
// taken again changes nothing.
func (o *Outbox) complete(ctx context.Context, c *claim, messageID string, sendErr error) error {
	const fence = ` WHERE id = $1 AND status = 'sending' AND attempts = $2`
	id, n := c.msg.OutboxID, c.attempts
	log := o.logger().With("id", id, "stream", c.msg.Stream, "kind", c.msg.Kind, "attempt", n)
	var err error
	switch {
	case sendErr == nil:
		_, err = o.Pool.Exec(ctx, `UPDATE email_outbox SET status = 'sent', ses_message_id = $3, sent_at = $4`+fence,
			id, n, messageID, o.Clock.Now())
	case IsPermanent(sendErr):
		log.Warn("outbox: permanent send failure; giving up", "err", sendErr)
		_, err = o.Pool.Exec(ctx, `UPDATE email_outbox SET status = 'dead', last_error = $3`+fence, id, n, sendErr.Error())
	case n >= maxAttempts(c.msg.Stream):
		o.logFailure(log, sendErr, "outbox: send failed on the last try; giving up")
		_, err = o.Pool.Exec(ctx, `UPDATE email_outbox SET status = 'dead', last_error = $3`+fence, id, n, sendErr.Error())
	default:
		o.logFailure(log, sendErr, "outbox: send failed; will retry")
		next := o.Clock.Now().Add(o.retryWait(c.msg.Stream, n))
		_, err = o.Pool.Exec(ctx, `UPDATE email_outbox SET status = 'failed', last_error = $3, next_attempt_at = $4`+fence,
			id, n, sendErr.Error(), next)
	}
	return err
}

// logFailure logs a temporary failure: an account-wide one as an error,
// any other as a warning (decisions.md "Outbox permanent send errors").
func (o *Outbox) logFailure(log *slog.Logger, err error, msg string) {
	if IsAccountWide(err) {
		log.Error(msg+" (account-wide)", "err", err)
		return
	}
	log.Warn(msg, "err", err)
}

// retryWait is the wait after failed attempt n (from 1), varied by ±20%.
func (o *Outbox) retryWait(stream string, n int) time.Duration {
	r := rand.Float64
	if o.Rand != nil {
		r = o.Rand
	}
	w := retryWaits[stream][n-1]
	return time.Duration(float64(w) * (0.8 + 0.4*r()))
}

func (o *Outbox) pollInterval() time.Duration {
	if o.PollInterval > 0 {
		return o.PollInterval
	}
	return DefaultPollInterval
}

func (o *Outbox) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}
