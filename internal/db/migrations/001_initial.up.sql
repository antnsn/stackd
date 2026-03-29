CREATE TABLE IF NOT EXISTS repos (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    url           TEXT NOT NULL,
    branch        TEXT NOT NULL DEFAULT 'main',
    remote        TEXT NOT NULL DEFAULT 'origin',
    auth_type     TEXT NOT NULL DEFAULT 'none',
    ssh_key_id    TEXT,
    pat_enc       TEXT,
    stacks_dir    TEXT NOT NULL DEFAULT '.',
    sync_interval INTEGER NOT NULL DEFAULT 60,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ssh_keys (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    private_key_enc TEXT NOT NULL,
    public_key      TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT,
    sensitive  INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO settings (key, value, sensitive) VALUES
    ('infisical_token',  '', 1),
    ('infisical_env',    'prod', 0),
    ('infisical_url',    '', 0),
    ('git_user_name',    'stackd', 0),
    ('git_user_email',   'stackd@localhost', 0),
    ('pull_only',        'false', 0)
ON CONFLICT (key) DO NOTHING;
