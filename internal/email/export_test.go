package email

import "context"

// Claimed is a row a test took through the real claim path without
// sending it, standing in for a poller that crashed or hung mid-send.
type Claimed = *claim

// Claim takes the next due row on stream as the poller would, without
// sending it. ok is false when nothing was due.
func Claim(ctx context.Context, o *Outbox, stream string) (c Claimed, ok bool, err error) {
	return o.claim(ctx, stream)
}

// Complete records a send's outcome for c through the real, fenced path.
func Complete(ctx context.Context, o *Outbox, c Claimed, messageID string, sendErr error) error {
	return o.complete(ctx, c, messageID, sendErr)
}

// SetOnWait makes Run call f each time it has drained and is about to wait.
func SetOnWait(o *Outbox, f func()) { o.onWait = f }
