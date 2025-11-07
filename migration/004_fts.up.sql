-- Full-Text Search for file manager files
-- Virtual table and triggers to keep it in sync with non-deleted rows

-- Full-Text Search virtual table (external content) over filename, tags, and description
CREATE VIRTUAL TABLE IF NOT EXISTS edev_core_filemanager_files_fts
USING fts5(
    filename,
    filetag,
    filedescription,
    content='edev_core_filemanager_files',
    content_rowid='id'
);

-- Populate FTS index for existing non-deleted rows
INSERT INTO edev_core_filemanager_files_fts(rowid, filename, filetag, filedescription)
  SELECT id, filename, filetag, filedescription
  FROM edev_core_filemanager_files
  WHERE deleted = 0;

-- Triggers to keep FTS in sync (insert/update/delete; ignore deleted rows)
CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_ai
AFTER INSERT ON edev_core_filemanager_files
BEGIN
  INSERT INTO edev_core_filemanager_files_fts(rowid, filename, filetag, filedescription)
    SELECT NEW.id, NEW.filename, NEW.filetag, NEW.filedescription WHERE NEW.deleted = 0;
END;

CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_ad
AFTER DELETE ON edev_core_filemanager_files
BEGIN
  INSERT INTO edev_core_filemanager_files_fts(edev_core_filemanager_files_fts, rowid)
    VALUES('delete', OLD.id);
END;

CREATE TRIGGER IF NOT EXISTS edev_core_filemanager_files_au
AFTER UPDATE OF filename, filetag, filedescription, deleted ON edev_core_filemanager_files
BEGIN
  -- remove old index entry
  INSERT INTO edev_core_filemanager_files_fts(edev_core_filemanager_files_fts, rowid)
    VALUES('delete', OLD.id);
  -- add new entry only if not deleted
  INSERT INTO edev_core_filemanager_files_fts(rowid, filename, filetag, filedescription)
    SELECT NEW.id, NEW.filename, NEW.filetag, NEW.filedescription WHERE NEW.deleted = 0;
END;
