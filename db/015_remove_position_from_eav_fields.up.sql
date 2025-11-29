PRAGMA foreign_keys = OFF;

CREATE TABLE eav_fields_new (
    id               INTEGER PRIMARY KEY,
    form_id          INTEGER NOT NULL REFERENCES eav_forms(id) ON DELETE CASCADE,

    machine_name     TEXT NOT NULL,
    label            TEXT NOT NULL,

    z_order          INTEGER NOT NULL DEFAULT 0,
    visible          INTEGER NOT NULL DEFAULT 1 CHECK (visible IN (0,1)),

    is_ui            INTEGER NOT NULL DEFAULT 0 CHECK (is_ui IN (0,1)),
    ui_role          TEXT,

    is_subform       INTEGER NOT NULL DEFAULT 0 CHECK (is_subform IN (0,1)),
    subform_form_id  INTEGER REFERENCES eav_forms(id) ON DELETE CASCADE,

    primitive_kind   TEXT NOT NULL CHECK (primitive_kind IN (
        'BOOL',
        'DATETIME',
        'FLOAT',
        'INT',
        'TEXT'
    )),
    ui_kind          TEXT NOT NULL,
    ui_meta_json     TEXT,

    is_readonly      INTEGER NOT NULL DEFAULT 0 CHECK (is_readonly IN (0,1)),
    required         INTEGER NOT NULL DEFAULT 0 CHECK (required IN (0,1)),

    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (form_id, machine_name)
);

INSERT INTO eav_fields_new (
    id,
    form_id,
    machine_name,
    label,
    z_order,
    visible,
    is_ui,
    ui_role,
    is_subform,
    subform_form_id,
    primitive_kind,
    ui_kind,
    ui_meta_json,
    is_readonly,
    required,
    created_at,
    updated_at
)
SELECT
    id,
    form_id,
    machine_name,
    label,
    z_order,
    visible,
    is_ui,
    ui_role,
    is_subform,
    subform_form_id,
    primitive_kind,
    ui_kind,
    ui_meta_json,
    COALESCE(is_readonly, 0),
    required,
    created_at,
    updated_at
FROM eav_fields;

DROP TABLE eav_fields;
ALTER TABLE eav_fields_new RENAME TO eav_fields;

CREATE INDEX IF NOT EXISTS idx_eav_fields_order
    ON eav_fields(form_id, z_order, label);

CREATE INDEX IF NOT EXISTS idx_eav_fields_form_machine
    ON eav_fields(form_id, machine_name);

PRAGMA foreign_keys = ON;
