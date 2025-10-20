-- Base system schema (SQLite)
-- Notes:
-- - INTEGER PRIMARY KEY uses the rowid for auto-increment behavior.
-- - Booleans are stored as INTEGER with CHECK constraint and defaults 0/1.
-- - CURRENT_TIMESTAMP yields UTC "YYYY-MM-DD HH:MM:SS" in SQLite.
-- - Foreign keys require PRAGMA foreign_keys=ON (enabled in driver DSN).

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    reference_id TEXT NOT NULL UNIQUE DEFAULT "", -- a trigger will set this to a UUID
    username TEXT UNIQUE COLLATE NOCASE,
    email TEXT UNIQUE COLLATE NOCASE,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
    avatar_url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_username_nocase
    ON users(LOWER(username)) WHERE username IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_email_nocase
    ON users(LOWER(email)) WHERE email IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_enabled ON users(enabled);
CREATE INDEX IF NOT EXISTS idx_users_reference_id ON users(reference_id);

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
AFTER UPDATE OF username, email, enabled, avatar_url ON users
BEGIN
    UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

CREATE TRIGGER IF NOT EXISTS identities_set_updated_at
AFTER UPDATE OF user_id, provider, provider_uid, avatar_url ON identities
BEGIN
    UPDATE identities SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

CREATE TRIGGER IF NOT EXISTS users_reference_uuid
AFTER INSERT ON users
BEGIN
  UPDATE users
  SET reference_id = ( 
    select substr(u,1,8)||'-'|| 
    substr(u,9,4)||'-4'|| 
    substr(u,13,3)||'-'||v|| 
    substr(u,17,3)||'-'|| 
    substr(u,21,12) from ( 
        select 
            lower(hex(randomblob(16))) as u, 
            substr('89ab',abs(random()) % 4 + 1, 1) as v)
    )
  WHERE id = NEW.id;
END;

--

CREATE TABLE IF NOT EXISTS magic_token (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    action TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL DEFAULT (DATETIME('now', '+3 hour'))
);


