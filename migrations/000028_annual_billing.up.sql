-- Yearly billing: Pro and Agency can also be bought for a year at a time, at two months free. The yearly price is advertised from here, like the
-- monthly one, and the billing service refuses checkout for it while Stripe's yearly price disagrees (internal/billing/prices.go).
ALTER TABLE plans ADD COLUMN annual_price_cents integer CHECK (annual_price_cents >= 0); -- null: not sold yearly
UPDATE plans SET annual_price_cents = 19000 WHERE code = 'pro';
UPDATE plans SET annual_price_cents = 59000 WHERE code = 'agency';

-- Whether the current subscription is paid monthly or yearly, from the price Stripe reports. Null for the free plan and for rows written
-- before this column existed (all of which were monthly; the next subscription event fills it in).
ALTER TABLE subscriptions ADD COLUMN billing_interval text CHECK (billing_interval IN ('month', 'year'));
UPDATE subscriptions s SET billing_interval = 'month' FROM plans p WHERE p.id = s.plan_id AND p.code <> 'free' AND s.provider_subscription_id IS NOT NULL;
