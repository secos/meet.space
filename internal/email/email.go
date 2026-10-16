// Package email holds the Sender interface and the outbox every email goes
// through. Providers (the dev stdout sender now, SES in Phase 6) sit
// behind Sender, so one can be swapped for another.
package email

import (
	"context"

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

// Sender delivers one message and returns the provider's message ID.
type Sender interface {
	Send(ctx context.Context, m Message) (messageID string, err error)
}
