DROP INDEX users_lifecycle_candidates_idx;
DROP INDEX users_unsubscribe_token_key;
ALTER TABLE users DROP COLUMN unsubscribe_token, DROP COLUMN lifecycle_opt_out_at;
DROP TABLE lifecycle_emails;
