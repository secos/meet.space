package fakes

import (
	"context"
	"fmt"

	"meet.space/internal/payments"
)

// PaymentsCall is one call to Payments.
type PaymentsCall struct {
	Meta
	// Checkout is what was passed to CreateCheckoutSession.
	Checkout payments.CheckoutParams
	// CustomerID and ReturnURL are what was passed to CreatePortalSession.
	CustomerID string
	ReturnURL  string
	// SubscriptionID and Cancel are what was passed to
	// SetCancelAtPeriodEnd (Cancel) or CancelSubscription.
	SubscriptionID string
	Cancel         bool
	// URL is what CreateCheckoutSession or CreatePortalSession returned,
	// once Done with no error.
	URL string
}

func (c *PaymentsCall) clone() PaymentsCall {
	d := *c
	d.Checkout = copyCheckout(c.Checkout)
	return d
}

// Payments is a recording payments.Payments. By default the kth successful
// checkout returns https://checkout.stripe.test/session/k and the kth
// successful portal session https://billing.stripe.test/session/k. The
// .test domain is reserved, so these never reach a real server.
type Payments struct {
	recorder[PaymentsCall, *PaymentsCall]
	checkoutURLs func(k int, p payments.CheckoutParams) string
	portalURLs   func(k int, customerID, returnURL string) string
	checkouts    int
	portals      int
}

// CreateCheckoutSession records p and returns a redirect URL.
func (f *Payments) CreateCheckoutSession(ctx context.Context, p payments.CheckoutParams) (string, error) {
	c := f.do(ctx, "CreateCheckoutSession", PaymentsCall{Checkout: copyCheckout(p)}, func(c *PaymentsCall) error {
		f.checkouts++
		if f.checkoutURLs != nil {
			c.URL = f.checkoutURLs(f.checkouts, copyCheckout(c.Checkout))
		} else {
			c.URL = fmt.Sprintf("https://checkout.stripe.test/session/%d", f.checkouts)
		}
		return nil
	})
	return c.URL, c.Err
}

// CreatePortalSession records the customer and return URL and returns a
// portal URL.
func (f *Payments) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	c := f.do(ctx, "CreatePortalSession", PaymentsCall{CustomerID: customerID, ReturnURL: returnURL}, func(c *PaymentsCall) error {
		f.portals++
		if f.portalURLs != nil {
			c.URL = f.portalURLs(f.portals, customerID, returnURL)
		} else {
			c.URL = fmt.Sprintf("https://billing.stripe.test/session/%d", f.portals)
		}
		return nil
	})
	return c.URL, c.Err
}

// SetCancelAtPeriodEnd records the change.
func (f *Payments) SetCancelAtPeriodEnd(ctx context.Context, subscriptionID string, cancel bool) error {
	return f.do(ctx, "SetCancelAtPeriodEnd", PaymentsCall{SubscriptionID: subscriptionID, Cancel: cancel}, nil).Err
}

// CancelSubscription records the cancellation.
func (f *Payments) CancelSubscription(ctx context.Context, subscriptionID string) error {
	return f.do(ctx, "CancelSubscription", PaymentsCall{SubscriptionID: subscriptionID}, nil).Err
}

// SetCheckoutURLs chooses the URL returned by each successful checkout; k
// counts successful checkouts from 1. urls runs while the fake is locked,
// so it must not call the fake's methods.
func (f *Payments) SetCheckoutURLs(urls func(k int, p payments.CheckoutParams) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkoutURLs = urls
}

// SetPortalURLs chooses the URL returned by each successful portal
// session; k counts successful portal sessions from 1. urls runs while the
// fake is locked, so it must not call the fake's methods.
func (f *Payments) SetPortalURLs(urls func(k int, customerID, returnURL string) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.portalURLs = urls
}

func copyCheckout(p payments.CheckoutParams) payments.CheckoutParams {
	if p.TrialEnd != nil {
		t := *p.TrialEnd
		p.TrialEnd = &t
	}
	return p
}
