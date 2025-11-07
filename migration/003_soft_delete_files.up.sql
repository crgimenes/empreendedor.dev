-- Soft delete support for edev_core_filemanager_files
-- Adds a 'deleted' flag and triggers/indexes, and ensures updated_at changes when toggled.

ALTER TABLE edev_core_filemanager_files
    ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0,1));

-- Helpful index for frequent queries that filter by user and non-deleted
CREATE INDEX IF NOT EXISTS idx_edev_core_filemanager_files_user_id_deleted
    ON edev_core_filemanager_files(user_id, deleted);

-- Ensure updated_at changes when deleted flag is toggled
CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_set_updated_at_on_deleted
AFTER UPDATE OF deleted ON edev_core_filemanager_files
BEGIN
    UPDATE edev_core_filemanager_files
    SET updated_at = CURRENT_TIMESTAMP
    WHERE id = OLD.id;
END;
