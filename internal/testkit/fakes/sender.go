package fakes

import (
	"context"
	"fmt"
	"maps"

	"meet.space/internal/email"
)

// SenderCall is one call to Sender.Send.
type SenderCall struct {
	Meta
	// Message is the whole message as passed to Send.
	Message email.Message
	// MessageID is the ID Send returned, once Done with no error.
	MessageID string
}

func (c *SenderCall) clone() SenderCall {
	d := *c
	d.Message = copyMessage(c.Message)
	return d
}

// Sender is a recording email.Sender. A successful Send returns a message
// ID; by default the kth successful send gets "fake-k".
type Sender struct {
	recorder[SenderCall, *SenderCall]
	ids  func(k int, m email.Message) string
	sent int
}

// Send records m and, unless the call is made to fail, returns an ID.
func (s *Sender) Send(ctx context.Context, m email.Message) (string, error) {
	c := s.do(ctx, "Send", SenderCall{Message: copyMessage(m)}, func(c *SenderCall) error {
		s.sent++
		if s.ids != nil {
			c.MessageID = s.ids(s.sent, copyMessage(c.Message))
		} else {
			c.MessageID = fmt.Sprintf("fake-%d", s.sent)
		}
		return nil
	})
	return c.MessageID, c.Err
}

// SetMessageIDs chooses the ID returned by each successful send; k counts
// successful sends from 1. ids runs while the fake is locked, so it must
// not call the fake's methods.
func (s *Sender) SetMessageIDs(ids func(k int, m email.Message) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = ids
}

// Sent returns the messages whose Send succeeded, in the order Send was
// called.
func (s *Sender) Sent() []email.Message {
	var out []email.Message
	for _, c := range s.Calls() {
		if c.Done && c.Err == nil {
			out = append(out, c.Message)
		}
	}
	return out
}

func copyMessage(m email.Message) email.Message {
	if m.GroupID != nil {
		g := *m.GroupID
		m.GroupID = &g
	}
	if m.Headers != nil {
		m.Headers = maps.Clone(m.Headers)
	}
	return m
}
