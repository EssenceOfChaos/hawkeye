-- Public uptime badges, opt-in per monitor. A badge is addressed by a random token (never the monitor's id): turning it off clears the token, so the
-- address stops working, and turning it on again issues a new one.
ALTER TABLE monitors ADD COLUMN badge_token text UNIQUE CHECK (badge_token ~ '^[A-Za-z0-9_-]{22}$');
