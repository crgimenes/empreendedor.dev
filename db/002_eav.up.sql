-- - Workspaces (neutral container)
-- - Forms / Fields (with z-order, UI-only, subform)
-- - Records / Values (with optimistic locking via record.rev)
-- No triggers here. The application enforces business rules.

PRAGMA foreign_keys = ON;

-- =========================================
-- USERS (referenced): expected from 001_base_system.up.sql
-- Table: users(id) must exist.
-- =========================================

-- =========================================
-- WORKSPACES (neutral container)
-- =========================================
CREATE TABLE IF NOT EXISTS workspaces (
    id            INTEGER PRIMARY KEY,
    reference_id  TEXT UNIQUE,                -- opaque external ID (e.g., UUIDv7/ULID)
    name          TEXT NOT NULL,
    description   TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_workspaces_name
    ON workspaces(name);

-- =========================================
-- FORMS (EAV schema root)
-- =========================================
CREATE TABLE IF NOT EXISTS eav_forms (
    id             INTEGER PRIMARY KEY,
    reference_id   TEXT UNIQUE,               -- opaque external ID
    workspace_id   INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    owner_user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    slug           TEXT NOT NULL,             -- stable per-workspace identifier
    label          TEXT NOT NULL,             -- display name
    active         INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (workspace_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_eav_forms_workspace
    ON eav_forms(workspace_id);

CREATE INDEX IF NOT EXISTS idx_eav_forms_owner
    ON eav_forms(owner_user_id);

-- =========================================
-- FIELDS (attribute definitions)
-- - z_order: primary render order; ties break by label ASC.
-- - is_ui/ui_role: UI-only elements (separator, button, note); no value rows expected.
-- - is_subform/subform_form_id: declares a child form; linkage done in records.*parent_*.
-- - primitive_kind: storage/validation type.
-- - ui_kind/ui_meta_json: frontend rendering hints.
-- =========================================
CREATE TABLE IF NOT EXISTS eav_fields (
    id               INTEGER PRIMARY KEY,
    form_id          INTEGER NOT NULL REFERENCES eav_forms(id) ON DELETE CASCADE,

    machine_name     TEXT NOT NULL,           -- internal stable key (no spaces), unique per form
    label            TEXT NOT NULL,           -- user-facing label

    z_order          INTEGER NOT NULL DEFAULT 0,  -- primary render order
    position         INTEGER NOT NULL DEFAULT 0,  -- secondary/legacy ordering (optional)
    visible          INTEGER NOT NULL DEFAULT 1 CHECK (visible IN (0,1)),

    -- UI-only elements (no data persisted in values)
    is_ui            INTEGER NOT NULL DEFAULT 0 CHECK (is_ui IN (0,1)),
    ui_role          TEXT,                    -- e.g., 'separator','button','note'

    -- Subform declaration (child form reference)
    is_subform       INTEGER NOT NULL DEFAULT 0 CHECK (is_subform IN (0,1)),
    subform_form_id  INTEGER REFERENCES eav_forms(id) ON DELETE CASCADE,

    primitive_kind   TEXT NOT NULL CHECK (primitive_kind IN
                        (
                            'BOOL',
                            'DATETIME',
                            'FLOAT',
                            'INT',
                            'TEXT'
                        )),
    ui_kind          TEXT NOT NULL,           -- e.g., text_input, textarea, checkbox, select...
    ui_meta_json     TEXT,                    -- JSON with UI params (rows, placeholder, options...)

    required         INTEGER NOT NULL DEFAULT 0 CHECK (required IN (0,1)),

    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (form_id, machine_name)
);

-- Helpful order for rendering lists of fields:
CREATE INDEX IF NOT EXISTS idx_eav_fields_order
    ON eav_fields(form_id, z_order, label);

CREATE INDEX IF NOT EXISTS idx_eav_fields_form_machine
    ON eav_fields(form_id, machine_name);

-- =========================================
-- RECORDS (entities/rows)
-- - rev: optimistic-lock counter (increment on successful update).
-- - parent_record_id/parent_field_id: subform linkage (child → parent).
-- =========================================
CREATE TABLE IF NOT EXISTS eav_records (
    id               INTEGER PRIMARY KEY,
    reference_id     TEXT UNIQUE,             -- opaque external ID
    form_id          INTEGER NOT NULL REFERENCES eav_forms(id) ON DELETE CASCADE,
    workspace_id     INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,

    owner_user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    tags_json        TEXT,                    -- optional JSON tags

    -- Subform linkage (child record points to its parent)
    parent_record_id INTEGER REFERENCES eav_records(id) ON DELETE CASCADE,
    parent_field_id  INTEGER REFERENCES eav_fields(id)  ON DELETE CASCADE,

    rev              INTEGER NOT NULL DEFAULT 1, -- optimistic locking
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at       DATETIME                  -- NULL means not deleted
);

CREATE INDEX IF NOT EXISTS idx_eav_records_form_updated
    ON eav_records(form_id, updated_at);

CREATE INDEX IF NOT EXISTS idx_eav_records_workspace_form
    ON eav_records(workspace_id, form_id, updated_at);

CREATE INDEX IF NOT EXISTS idx_eav_records_owner
    ON eav_records(owner_user_id);

CREATE INDEX IF NOT EXISTS idx_eav_records_parent
    ON eav_records(parent_record_id);

CREATE INDEX IF NOT EXISTS idx_eav_records_parent_field
    ON eav_records(parent_field_id);

-- =========================================
-- VALUES (vertical, typed)
-- - Exactly one of value_* should be non-NULL per row (enforced by the app in this MVP).
-- - form_id is denormalized for convenience (guards/filtering).
-- =========================================
CREATE TABLE IF NOT EXISTS eav_values (
    record_id      INTEGER NOT NULL REFERENCES eav_records(id) ON DELETE CASCADE,
    field_id       INTEGER NOT NULL REFERENCES eav_fields(id)  ON DELETE CASCADE,
    form_id        INTEGER NOT NULL REFERENCES eav_forms(id)   ON DELETE CASCADE,

    value_bool     INTEGER CHECK (value_bool IN (0,1)),
    value_datetime TEXT,                      -- ISO-8601 (UTC recommended)
    value_float    REAL,
    value_int      INTEGER,
    value_text     TEXT,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (record_id, field_id)
);

CREATE INDEX IF NOT EXISTS idx_eav_values_record
    ON eav_values(record_id);

