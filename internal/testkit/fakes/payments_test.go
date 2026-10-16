package fakes_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"meet.space/internal/payments"
	"meet.space/internal/testkit/fakes"
)

// Each Payments call is recorded with its arguments; Checkout and portal
// sessions return chosen URLs (by default on the reserved .test domain).
//
// P0-HARNESS-06, P0-HARNESS-07
func TestPayments_RecordsArgumentsAndReturnsChosenURLs(t *testing.T) {
	ctx := context.Background()
	f := &fakes.Payments{}
	trialEnd := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	p := payments.CheckoutParams{
		GroupID:       uuid.MustParse("0192f1d2-0000-7000-8000-000000000001"),
		PayerUserID:   uuid.MustParse("0192f1d2-0000-7000-8000-000000000002"),
		PriceID:       "price_yearly",
		CustomerEmail: "owner@example.com",
		SuccessURL:    "https://hikers.example.com/admin/groups/hikers/settings",
		CancelURL:     "https://hikers.example.com/admin/groups/hikers/settings",
		TrialEnd:      &trialEnd,
	}
	u1, err := f.CreateCheckoutSession(ctx, p)
	if err != nil || u1 != "https://checkout.stripe.test/session/1" {
		t.Errorf("checkout 1 = %q, %v", u1, err)
	}
	f.SetCheckoutURLs(func(k int, p payments.CheckoutParams) string {
		return fmt.Sprintf("https://pay.test/%s/%d", p.PriceID, k)
	})
	if u2, _ := f.CreateCheckoutSession(ctx, p); u2 != "https://pay.test/price_yearly/2" {
		t.Errorf("checkout 2 = %q", u2)
	}
	if u, _ := f.CreatePortalSession(ctx, "cus_123", "https://meet.space/admin/groups/hikers/settings"); u != "https://billing.stripe.test/session/1" {
		t.Errorf("portal = %q", u)
	}
	f.SetPortalURLs(func(k int, customerID, _ string) string {
		return fmt.Sprintf("https://portal.test/%s/%d", customerID, k)
	})
	if u, _ := f.CreatePortalSession(ctx, "cus_456", "https://meet.space/"); u != "https://portal.test/cus_456/2" {
		t.Errorf("portal 2 = %q", u)
	}
	_ = f.SetCancelAtPeriodEnd(ctx, "sub_old", true)
	_ = f.CancelSubscription(ctx, "sub_gone")

	// Changing the caller's TrialEnd afterwards doesn't change the record.
	trialEnd = trialEnd.Add(time.Hour)

	calls := f.Calls()
	if len(calls) != 6 {
		t.Fatalf("%d calls, want 6", len(calls))
	}
	c := calls[0]
	if c.Method != "CreateCheckoutSession" || c.Checkout.GroupID != p.GroupID || c.Checkout.PayerUserID != p.PayerUserID ||
		c.Checkout.PriceID != "price_yearly" || c.Checkout.CustomerEmail != "owner@example.com" ||
		c.Checkout.SuccessURL != p.SuccessURL || c.Checkout.CancelURL != p.CancelURL ||
		c.Checkout.TrialEnd == nil || !c.Checkout.TrialEnd.Equal(time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)) ||
		c.URL != "https://checkout.stripe.test/session/1" {
		t.Errorf("checkout call = %+v (TrialEnd %v)", c, c.Checkout.TrialEnd)
	}
	if c := calls[2]; c.Method != "CreatePortalSession" || c.CustomerID != "cus_123" || c.ReturnURL != "https://meet.space/admin/groups/hikers/settings" {
		t.Errorf("portal call = %+v", c)
	}
	if c := calls[4]; c.Method != "SetCancelAtPeriodEnd" || c.SubscriptionID != "sub_old" || !c.Cancel {
		t.Errorf("cancel-at-period-end call = %+v", c)
	}
	if c := calls[5]; c.Method != "CancelSubscription" || c.SubscriptionID != "sub_gone" {
		t.Errorf("cancel call = %+v", c)
	}

	// A returned record's TrialEnd is a copy too.
	*calls[0].Checkout.TrialEnd = time.Time{}
	if te := f.Calls()[0].Checkout.TrialEnd; te.IsZero() {
		t.Error("changing a returned record changed the fake")
	}
}

// A failed Checkout returns no URL and the error; the next one succeeds and
// is numbered as the first successful checkout.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestPayments_FailedCallsReturnTheChosenError(t *testing.T) {
	ctx := context.Background()
	f := &fakes.Payments{}
	errCard := errors.New("stripe unavailable")
	f.FailNth(1, errCard)
	f.FailWhen(func(c fakes.PaymentsCall) bool { return c.Method == "CancelSubscription" }, errBoom)
	if u, err := f.CreateCheckoutSession(ctx, payments.CheckoutParams{}); u != "" || !errors.Is(err, errCard) {
		t.Errorf("checkout = %q, %v; want no URL and the error", u, err)
	}
	if u, err := f.CreateCheckoutSession(ctx, payments.CheckoutParams{}); err != nil || u != "https://checkout.stripe.test/session/1" {
		t.Errorf("retry = %q, %v", u, err)
	}
	if err := f.CancelSubscription(ctx, "sub_1"); !errors.Is(err, errBoom) {
		t.Errorf("cancel = %v, want boom", err)
	}
	if c := f.Calls()[0]; !errors.Is(c.Err, errCard) || c.URL != "" {
		t.Errorf("recorded failed checkout = %+v", c)
	}
}
