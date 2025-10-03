-- Drop tables if they exist (SQLite)
-- Note: SQLite does not support CASCADE in DROP TABLE; foreign keys are enforced on DELETE only.
DROP TRIGGER IF EXISTS identities_set_updated_at;
DROP TRIGGER IF EXISTS users_set_updated_at;
DROP INDEX IF EXISTS idx_identities_user_id;
DROP TABLE IF EXISTS identities;
DROP TABLE IF EXISTS users;

