-- forum 

CREATE TABLE forum (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id TEXT UNIQUE NOT NULL, -- opaque external identifier
    tenant_id INTEGER NOT NULL,
    workspace_id INTEGER NOT NULL,
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE forum_threads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id TEXT UNIQUE NOT NULL, -- opaque external identifier
    forum_id INTEGER NOT NULL REFERENCES forum(id) ON DELETE CASCADE,
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE forum_posts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id TEXT UNIQUE NOT NULL, -- opaque external identifier
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    content TEXT NOT NULL, -- markdown or rich text content
    parent_post_id INTEGER REFERENCES forum_posts(id) ON DELETE CASCADE, -- for replies
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);


