-- AI-drafted incident updates: a plan feature (Pro and Agency) that the organization's owner turns on; off until they do. Drafts are written on
-- request and never stored or posted by HawkEye (internal/incidentdraft).
ALTER TABLE plans ADD COLUMN ai_drafts_enabled boolean NOT NULL DEFAULT false;
UPDATE plans SET ai_drafts_enabled = true WHERE code IN ('pro', 'agency');
ALTER TABLE organizations ADD COLUMN ai_drafts_enabled boolean NOT NULL DEFAULT false;
