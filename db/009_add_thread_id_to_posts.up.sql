-- Add thread_id column to forum_posts table to properly link posts to threads
ALTER TABLE forum_posts ADD COLUMN thread_id INTEGER REFERENCES forum_threads(id) ON DELETE CASCADE;

-- Create index for faster lookups
CREATE INDEX idx_forum_posts_thread_id ON forum_posts(thread_id);
