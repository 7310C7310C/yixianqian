-- Dual-role users: a v1 user row and an observer row may share one open_id.
-- Keep one users row and remember the observer capability on it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS observer_enabled boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS users_observer_enabled_idx ON users (observer_enabled) WHERE observer_enabled;
