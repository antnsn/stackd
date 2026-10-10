CREATE TABLE IF NOT EXISTS hosts (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    docker_host TEXT NOT NULL DEFAULT '',
    ssh_key_id  TEXT,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Seed the built-in "local" host with a stable, well-known id so stack/repo
-- host references never dangle. docker_host='' means "use the local Docker
-- socket" (the pre-multi-host behaviour). ON CONFLICT keeps this idempotent and
-- portable across sqlite and postgres.
INSERT INTO hosts (id, name, docker_host, enabled) VALUES
    ('local', 'local', '', 1)
ON CONFLICT (id) DO NOTHING;

-- Existing repos default to the local host, preserving current behaviour.
ALTER TABLE repos ADD COLUMN host_id TEXT NOT NULL DEFAULT 'local';

-- Per-stack host overrides. A missing row means the stack inherits its repo's
-- host. Keyed by (repo_id, stack_name); a stack is identified by its directory
-- name within the repo.
CREATE TABLE IF NOT EXISTS stack_overrides (
    repo_id    TEXT NOT NULL,
    stack_name TEXT NOT NULL,
    host_id    TEXT NOT NULL,
    PRIMARY KEY (repo_id, stack_name)
);
