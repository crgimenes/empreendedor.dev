-- Backfill missing reference_id values in workspaces table
-- This ensures all workspaces have a unique reference_id for external use

UPDATE workspaces
SET reference_id = lower(hex(randomblob(16)))
WHERE reference_id IS NULL OR reference_id = '';
