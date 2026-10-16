// Package payments is the interface to the payment provider's API (Stripe
// in production): starting a Checkout for the $20/year subscription,
// opening the customer portal, and changing or ending a subscription.
//
// Webhooks are not part of this interface: /webhooks/stripe verifies the
// signature itself (Security Rule 11) and reads the event from the
// request.
//
// Phase 0 has only the interface and the recording fake in
// internal/testkit/fakes. The Stripe client arrives in Phase 10; only
// Stripe's real webhooks prove it, so a passing fake is never evidence
// for it.
package payments

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// CheckoutParams describes one Checkout session for a group's upgrade.
type CheckoutParams struct {
	// GroupID and PayerUserID go into the session's metadata, so the
	// checkout.session.completed webhook knows which group to make paid.
	GroupID     uuid.UUID
	PayerUserID uuid.UUID

	PriceID       string
	CustomerEmail string

	// SuccessURL and CancelURL bring the payer back to the site they
	// started on.
	SuccessURL string
	CancelURL  string

	// TrialEnd is set only after an ownership transfer: the new owner's
	// subscription starts trialing until the old owner's ending
	// subscription's current_period_end, so nobody pays twice. Nil
	// otherwise.
	TrialEnd *time.Time
}

// Payments talks to the payment provider on the app's behalf.
type Payments interface {
	// CreateCheckoutSession starts a hosted Checkout and returns the URL
	// to redirect the payer to.
	CreateCheckoutSession(ctx context.Context, p CheckoutParams) (redirectURL string, err error)

	// CreatePortalSession opens the customer portal (card, invoices,
	// cancel) for a customer and returns its URL. The portal sends the
	// customer back to returnURL.
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)

	// SetCancelAtPeriodEnd sets or clears cancel_at_period_end on a
	// subscription. An ownership transfer sets it on the old owner's
	// subscription; the group keeps paid features until the period ends.
	SetCancelAtPeriodEnd(ctx context.Context, subscriptionID string, cancel bool) error

	// CancelSubscription ends a subscription now (when a group is
	// deleted).
	CancelSubscription(ctx context.Context, subscriptionID string) error
}
