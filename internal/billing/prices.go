package billing

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrPriceMismatch means Stripe's price for the plan does not match the price advertised on the site, so Checkout for it is refused rather than
// charging a customer something other than what they were shown.
var ErrPriceMismatch = errors.New("the payment provider's price does not match the advertised plan price")

// expectedCurrency is the currency the site advertises in: the plans table holds only cents, and every page shows "$".
const expectedCurrency = "usd"

// paidPlans are the plans that are bought through Checkout.
var paidPlans = []string{"pro", "agency"}

// PriceProblem is one disagreement between a plan and its provider price.
type PriceProblem struct {
	Plan    string
	Message string
}

// VerifyPrices asks the provider what each paid plan's price really charges and compares it with the plan's advertised price (the plans table,
// which is what the landing page and billing page display). The site shows one number and Stripe charges another only if someone creates or edits a
// Stripe price by hand, which nothing else would notice. While a plan has a problem, CreateCheckout refuses it, a gauge is set and the error tracker is told.
//
// A failed lookup (Stripe unreachable, a rate limit) proves nothing: it neither raises nor clears a problem, and checkout is not blocked by it.
func (s *Service) VerifyPrices(ctx context.Context) []PriceProblem {
	if !s.Enabled() {
		return nil
	}
	var found []PriceProblem
	for _, plan := range paidPlans {
		priceID, ok := s.cfg.PriceForPlan(plan)
		if !ok {
			continue
		}
		var cents int64
		if err := s.pool.QueryRow(ctx, `SELECT monthly_price_cents FROM plans WHERE code = $1`, plan).Scan(&cents); err != nil {
			s.log.WarnContext(ctx, "price check skipped: cannot read the plan", "plan", plan, "error", err)
			continue
		}
		got, err := s.provider.GetPrice(ctx, priceID)
		if err != nil {
			s.log.WarnContext(ctx, "price check skipped: cannot read the price from the payment provider", "plan", plan, "error", err)
			continue
		}
		s.setPriceProblem(ctx, plan, mismatch(plan, cents, got))
		if msg := s.priceProblem(plan); msg != "" {
			found = append(found, PriceProblem{Plan: plan, Message: msg})
		}
	}
	return found
}

// mismatch returns why the provider price is not the advertised one, or "" when it is.
func mismatch(plan string, advertisedCents int64, got PriceInfo) string {
	switch {
	case !got.Active:
		return fmt.Sprintf("the %s price in Stripe is archived (inactive), so Checkout would refuse it", plan)
	case !got.Recurring || got.Interval != "month" || got.IntervalCount != 1:
		return fmt.Sprintf("the %s price in Stripe is not a monthly recurring price (it is %s)", plan, describeInterval(got))
	case got.Currency != expectedCurrency:
		return fmt.Sprintf("the %s price in Stripe is in %s but the site advertises US dollars", plan, got.Currency)
	case got.UnitAmount != advertisedCents:
		return fmt.Sprintf("the %s plan is advertised at %s a month but Stripe charges %s", plan, dollars(advertisedCents), dollars(got.UnitAmount))
	}
	return ""
}

func describeInterval(p PriceInfo) string {
	if !p.Recurring {
		return "one-time"
	}
	return fmt.Sprintf("every %d %s", p.IntervalCount, p.Interval)
}

func dollars(cents int64) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

func (s *Service) priceProblem(plan string) string {
	s.priceMu.RLock()
	defer s.priceMu.RUnlock()
	return s.priceProblems[plan]
}

// setPriceProblem records the latest verdict for a plan, and reports a problem to the error tracker once when it first appears (not every hour).
func (s *Service) setPriceProblem(ctx context.Context, plan, msg string) {
	s.priceMu.Lock()
	if s.priceProblems == nil {
		s.priceProblems = map[string]string{}
	}
	before := s.priceProblems[plan]
	if msg == "" {
		delete(s.priceProblems, plan)
	} else {
		s.priceProblems[plan] = msg
	}
	s.priceMu.Unlock()

	gauge := 0.0
	if msg != "" {
		gauge = 1
	}
	s.m.BillingPriceMismatch.WithLabelValues(plan).Set(gauge)
	switch {
	case msg != "" && msg != before:
		s.log.ErrorContext(ctx, "PRICE MISMATCH: checkout for this plan is refused until Stripe and the plans table agree", "plan", plan, "problem", msg)
		s.rep.Capture(ctx, fmt.Errorf("billing price mismatch: %s", msg), map[string]string{"component": "billing", "plan": plan})
	case msg == "" && before != "":
		s.log.InfoContext(ctx, "price mismatch resolved: checkout is open again", "plan", plan)
	}
}

// WatchPrices verifies at startup and then every `every` until ctx ends: a price edited in Stripe later is caught within the hour.
func (s *Service) WatchPrices(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.VerifyPrices(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
