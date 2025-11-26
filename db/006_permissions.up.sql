-- ============================
-- Tenants (organizations, merchants, etc.)
-- ============================
CREATE TABLE IF NOT EXISTS tenants (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  reference_id TEXT
);

-- User <-> Tenant membership (many-to-many)
CREATE TABLE IF NOT EXISTS tenant_members (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(tenant_id, user_id)
);

-- ============================
-- Groups (each group belongs to only one tenant)
-- ============================
CREATE TABLE IF NOT EXISTS groups (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  UNIQUE(tenant_id, name) -- group names unique within a tenant
);

CREATE INDEX IF NOT EXISTS ix_groups_by_tenant ON groups(tenant_id);

-- ============================
-- Group memberships (scoped by tenant)
-- ============================
CREATE TABLE IF NOT EXISTS group_members (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  group_id  INTEGER NOT NULL REFERENCES groups(id)  ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(tenant_id, group_id, user_id)
);

CREATE INDEX IF NOT EXISTS ix_group_members_by_user  ON group_members(tenant_id, user_id);
CREATE INDEX IF NOT EXISTS ix_group_members_by_group ON group_members(tenant_id, group_id);

-- ============================
-- Allow-only permissions
-- resource: feature token (e.g., 'mesa.participar', 'menu.admin', 'forum.postar')
-- scope: optional refiner (e.g., 'mesa:t1', 'forum:f42'); NULL => global
-- Exactly one subject: (user_id XOR group_id)
-- 'allowed' defaults to 0 (false) to enable explicit deny lines later; set to 1 for allows.
-- ============================
CREATE TABLE IF NOT EXISTS permits (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id  INTEGER REFERENCES users(id)  ON DELETE CASCADE,
  group_id INTEGER REFERENCES groups(id) ON DELETE CASCADE,
  resource TEXT NOT NULL,    -- e.g., 'mesa.participar', 'mesa.convidar'
  scope    TEXT,             -- NULL = global; convention: 'type:id' (e.g., 'mesa:t1')
  allowed  INTEGER NOT NULL DEFAULT 0 CHECK (allowed IN (0,1)), -- 1=true (allow), 0=false (deny/disabled)
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK ( (user_id IS NOT NULL) <> (group_id IS NOT NULL) ) -- XOR: only one subject
);

-- Separate uniqueness for user/group per-tenant
CREATE UNIQUE INDEX IF NOT EXISTS ux_permits_user
  ON permits(tenant_id, user_id,  resource, scope)
  WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS ux_permits_group
  ON permits(tenant_id, group_id, resource, scope)
  WHERE group_id IS NOT NULL;

-- Lookups (quick queries by tenant/resource/scope)
CREATE INDEX IF NOT EXISTS ix_permits_lookup_user
  ON permits(tenant_id, resource, scope, user_id);

CREATE INDEX IF NOT EXISTS ix_permits_lookup_group
  ON permits(tenant_id, resource, scope, group_id);

CREATE INDEX IF NOT EXISTS ix_permits_scope_null
  ON permits(tenant_id, resource)
  WHERE scope IS NULL;

-- Admin-oriented extra lookups
CREATE INDEX IF NOT EXISTS ix_permits_by_user  ON permits(user_id);
CREATE INDEX IF NOT EXISTS ix_permits_by_group ON permits(group_id);

-- ============================
-- Permissions dictionary (for admin UI / documentation)
-- Optional hierarchy via parent_id (purely organizational)
-- ============================
CREATE TABLE IF NOT EXISTS resources (
  id INTEGER PRIMARY KEY,
  parent_id INTEGER REFERENCES resources(id) ON DELETE SET NULL, -- hierarchy for UI grouping
  resource TEXT NOT NULL UNIQUE,  -- e.g., 'mesa.participar', 'mesa.convidar'
  label TEXT NOT NULL,            -- e.g., 'Participar de mesa', 'Convidar para mesa'
  scope_required INTEGER NOT NULL DEFAULT 0 
    CHECK (scope_required IN (0,1)), -- 0 = optional, 1 = required (UI hint)
  description TEXT
);

CREATE INDEX IF NOT EXISTS ix_resources_parent ON resources(parent_id);

-- ============================
-- Effective permits view (per-tenant):
-- Combines direct user permits and group-based permits into a single stream.
-- ============================
CREATE VIEW IF NOT EXISTS effective_permits AS
    -- Direct user permits
    SELECT
        p.tenant_id  AS tenant_id,
        p.user_id    AS user_id,
        p.resource   AS resource,
        p.scope      AS scope,
        p.allowed    AS allowed,
        p.created_at AS created_at
    FROM permits p
    WHERE p.user_id IS NOT NULL

    UNION ALL

    -- Group-derived permits (same tenant)
    SELECT
        p.tenant_id  AS tenant_id,
        gm.user_id   AS user_id,
        p.resource   AS resource,
        p.scope      AS scope,
        p.allowed    AS allowed,
        p.created_at AS created_at
    FROM permits p
    JOIN group_members gm
      ON gm.tenant_id = p.tenant_id
     AND gm.group_id  = p.group_id;

-- ============================
-- Example queries (permission checks)
-- ============================

-- Check a specific permission WITH scope (preferred path):
-- :tenant_id, :uid, :resource, :scope
-- SELECT 1
--  FROM effective_permits
--  WHERE tenant_id = :tenant_id
--    AND user_id   = :uid
--    AND resource  = :resource
--    AND scope     = :scope
--    AND allowed   = 1
--  LIMIT 1;

-- Fallback: check the GLOBAL permission (scope IS NULL):
-- SELECT 1
--  FROM effective_permits
--  WHERE tenant_id = :tenant_id
--    AND user_id   = :uid
--    AND resource  = :resource
--    AND scope IS NULL
--    AND allowed   = 1
--  LIMIT 1;

-- Explicit deny-overrides allow check (if implementing denies in future):
-- First check if there is an explicit deny for the same tuple; if found, treat as denied regardless of allow.
-- SELECT 1
--   FROM effective_permits
--  WHERE tenant_id = :tenant_id
--    AND user_id   = :uid
--    AND resource  = :resource
--    AND (scope = :scope OR (scope IS NULL AND :scope IS NULL))
--    AND allowed   = 0
--  LIMIT 1;

-- ============================
-- Example inserts
-- Remember: allowed defaults to 0, so set allowed=1 for "allow".
-- ============================

-- Admin view admin menu (global) for tenant 100 via group id=1:
-- INSERT INTO permits (tenant_id, group_id, resource, scope, allowed)
--   VALUES (100, 1, 'menu.admin', NULL, 1);

-- Game master (user 5) can invite players to table t1 in tenant 100:
-- INSERT INTO permits (tenant_id, user_id, resource, scope, allowed)
--   VALUES (100, 5, 'mesa.convidar', 'mesa:t1', 1);

-- Player 42 can join table t1 in tenant 100:
-- INSERT INTO permits (tenant_id, user_id, resource, scope, allowed)
--   VALUES (100, 42, 'mesa.participar', 'mesa:t1', 1);

-- Forum moderators group (id=10) can post in forum f42 in tenant 100:
-- INSERT INTO permits (tenant_id, group_id, resource, scope, allowed)
--   VALUES (100, 10, 'forum.postar', 'forum:f42', 1);

-- Example of toggling/deny (set allowed=0) without deleting the row:
-- UPDATE permits
--    SET allowed = 0
--  WHERE tenant_id = 100 
--    AND user_id = 42
--    AND resource = 'mesa.participar' AND scope = 'mesa:t1';

-- ============================
-- Notes
-- - Keep “implicit owner” out of permits (domain rule). For objects with owner_user_id,
--   grant the object’s basic actions in code (read/edit, invite/ban on own table).
-- - Always filter by tenant_id first in queries and endpoints.
-- - Standardize resource tokens (e.g., 'mesa.participar', 'forum.postar', 'menu.admin').
-- - Use scope as NULL (global) or 'type:id' (e.g., 'mesa:t1', 'forum:f42').
-- - 'allowed' enables toggling or future explicit deny. Current checks simply require allowed=1.

