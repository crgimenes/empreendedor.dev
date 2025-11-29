ALTER TABLE eav_fields
    ADD COLUMN is_readonly INTEGER NOT NULL DEFAULT 0 CHECK (is_readonly IN (0,1));
