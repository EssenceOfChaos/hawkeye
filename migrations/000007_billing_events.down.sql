DROP TABLE IF EXISTS billing_webhook_events;
DROP INDEX IF EXISTS subscriptions_provider_customer_key;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS last_synced_at;
