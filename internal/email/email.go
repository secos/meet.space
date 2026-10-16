// Package email holds the Sender interface and the outbox every email goes
// through. Providers (the dev stdout sender now, SES in Phase 6) sit
// behind Sender, so one can be swapped for another.
package email

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
)

// Streams. Login and invite mail goes on auth (the auth. subdomain);
// everything else goes on notify (the mail. subdomain).
const (
	StreamAuth   = "auth"
	StreamNotify = "notify"
)

// Message is one email ready to hand to a provider, exactly as stored in
// email_outbox plus the From address chosen for its stream.
type Message struct {
	OutboxID    uuid.UUID
	Stream      string
	Kind        string
	GroupID     *uuid.UUID
	To          string
	FromName    string
	FromAddress string
	Subject     string
	HTML        string
	Text        string
	Headers     map[string]string
}

// Sender delivers one message and returns the provider's message ID. An
// error is temporary unless wrapped with Permanent (decisions.md "Outbox
// permanent send errors"); wrap account-wide failures with AccountWide so
// they are logged as errors.
type Sender interface {
	Send(ctx context.Context, m Message) (messageID string, err error)
}

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as one no retry can fix: the provider rejected this
// message or address. The outbox gives the email up at once.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

type accountWideError struct{ err error }

func (e *accountWideError) Error() string { return e.err.Error() }
func (e *accountWideError) Unwrap() error { return e.err }

// AccountWide marks err as a failure of the whole sending account (paused,
// suspended, a sending domain not verified). It is still temporary: the
// email follows the retry schedule. The outbox logs it as an error.
func AccountWide(err error) error {
	if err == nil {
		return nil
	}
	return &accountWideError{err}
}

// IsAccountWide reports whether err was marked with AccountWide.
func IsAccountWide(err error) bool {
	var a *accountWideError
	return errors.As(err, &a)
}

// LogSender is the development Sender: it writes each email to W instead
// of sending it, so magic links can be read from the server's output. It
// is used when SES_REGION is empty.
type LogSender struct {
	mu sync.Mutex
	W  io.Writer
}

// Send writes m to W and returns "dev-" plus the outbox row's ID.
func (s *LogSender) Send(_ context.Context, m Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := fmt.Fprintf(s.W, "--- email %s (%s, %s)\nTo: %s\nFrom: %s <%s>\nSubject: %s\n\n%s\n--- end of email\n",
		m.OutboxID, m.Stream, m.Kind, m.To, m.FromName, m.FromAddress, m.Subject, m.Text)
	if err != nil {
		return "", err
	}
	return "dev-" + m.OutboxID.String(), nil
}
