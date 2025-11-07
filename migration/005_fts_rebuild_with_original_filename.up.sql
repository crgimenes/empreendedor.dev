-- Rebuild FTS to include original_filename in the index
-- We drop the previous external-content FTS table and triggers,
-- then recreate including original_filename so user-visible names are searchable.

-- Drop old triggers if they exist
DROP TRIGGER IF EXISTS edev_core_filemanager_files_ai;
DROP TRIGGER IF EXISTS edev_core_filemanager_files_ad;
DROP TRIGGER IF EXISTS edev_core_filemanager_files_au;

-- Drop old FTS virtual table
DROP TABLE IF EXISTS edev_core_filemanager_files_fts;

-- Create new FTS virtual table including original_filename
CREATE VIRTUAL TABLE IF NOT EXISTS edev_core_filemanager_files_fts
USING fts5(
    original_filename,
    filename,
    filetag,
    filedescription,
    content='edev_core_filemanager_files',
    content_rowid='id'
);

-- Backfill from non-deleted rows
INSERT INTO edev_core_filemanager_files_fts(rowid, original_filename, filename, filetag, filedescription)
  SELECT id, original_filename, filename, filetag, filedescription
  FROM edev_core_filemanager_files
  WHERE deleted = 0;

-- Triggers to keep FTS in sync
CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_ai
AFTER INSERT ON edev_core_filemanager_files
BEGIN
  INSERT INTO edev_core_filemanager_files_fts(rowid, original_filename, filename, filetag, filedescription)
    SELECT NEW.id, NEW.original_filename, NEW.filename, NEW.filetag, NEW.filedescription WHERE NEW.deleted = 0;
END;

CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_ad
AFTER DELETE ON edev_core_filemanager_files
BEGIN
  INSERT INTO edev_core_filemanager_files_fts(edev_core_filemanager_files_fts, rowid)
    VALUES('delete', OLD.id);
END;

CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_au
AFTER UPDATE OF original_filename, filename, filetag, filedescription, deleted ON edev_core_filemanager_files
BEGIN
  -- remove old index entry
  INSERT INTO edev_core_filemanager_files_fts(edev_core_filemanager_files_fts, rowid)
    VALUES('delete', OLD.id);
  -- add new entry only if not deleted
  INSERT INTO edev_core_filemanager_files_fts(rowid, original_filename, filename, filetag, filedescription)
    SELECT NEW.id, NEW.original_filename, NEW.filename, NEW.filetag, NEW.filedescription WHERE NEW.deleted = 0;
END;
