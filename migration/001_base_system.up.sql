-- Base system schema (SQLite)
-- Notes:
-- - INTEGER PRIMARY KEY uses the rowid for auto-increment behavior.
-- - Booleans are stored as INTEGER with CHECK constraint and defaults 0/1.
-- - CURRENT_TIMESTAMP yields UTC "YYYY-MM-DD HH:MM:SS" in SQLite.
-- - Foreign keys require PRAGMA foreign_keys=ON (enabled in driver DSN).

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS identities (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_uid TEXT NOT NULL,
    avatar_url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, provider_uid)
);

CREATE INDEX IF NOT EXISTS idx_identities_user_id ON identities(user_id);

CREATE TRIGGER IF NOT EXISTS users_set_updated_at
AFTER UPDATE OF username, enabled ON users
BEGIN
    UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

CREATE TRIGGER IF NOT EXISTS identities_set_updated_at
AFTER UPDATE OF user_id, provider, provider_uid, avatar_url ON identities
BEGIN
    UPDATE identities SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

