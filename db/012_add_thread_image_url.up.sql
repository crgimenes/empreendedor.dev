-- Add image_url column to forum_threads table
ALTER TABLE forum_threads ADD COLUMN image_url TEXT DEFAULT '';
