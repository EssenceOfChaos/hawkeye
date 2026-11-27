ALTER TABLE organizations DROP COLUMN ai_analysis_enabled;
ALTER TABLE plans DROP COLUMN max_ai_analyses;
DROP TABLE ai_analysis_usage;
DROP TABLE incident_analyses;
