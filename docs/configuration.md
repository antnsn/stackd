# Configuration

stackd is configured through environment variables. **Repositories, SSH keys, and Infisical credentials are managed through the Settings UI** (stored encrypted in the database) — not through environment variables.

The env vars below control runtime behaviour: logging, database, network, and authentication.

---

## Environment Variables

### Required

| Variable | Description |
|---|---|
| `SECRET_KEY` | Encryption key for all secrets stored in the database (SSH keys, tokens). **Required** — stackd will not start without it. Generate with `openssl rand -hex 32`. Keep this backed up: losing it makes stored secrets unrecoverable. |

### Database

| Variable | Default | Description |
|---|---|---|
| `DB_URL` | `sqlite://stackd.db` | Database connection string. Use `sqlite:///data/stackd.db` with a `/data` volume mount for reliable persistence. For PostgreSQL: `postgres://user:password@host:5432/stackd?sslmode=disable` |

> **Warning:** The default `sqlite://stackd.db` writes to the container's working directory. Always mount a volume and set an explicit `DB_URL` path in production or configuration will be lost on container restart.

### Server

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Port the dashboard and API listen on inside the container |
| `CLONE_DIR` | `/var/lib/stackd/repos` | Directory where stackd clones and stores repository working copies inside the container. You do not need to mount this unless you want to inspect the cloned files. |

### Authentication

| Variable | Default | Description |
|---|---|---|
| `DASHBOARD_TOKEN` | _(none)_ | Bearer token to protect all `/api/*` endpoints. If unset, the API is open. The token can also be set (and changed without restart) from the Settings UI — the env var takes precedence over the UI-saved value. |

### Security

| Variable | Default | Description |
|---|---|---|
| `SYNC_RATE_LIMIT_SECONDS` | `5` | Minimum seconds between manual sync requests per repo (prevents hammering) |

### Logging & Observability

| Variable | Default | Description |
|---|---|---|
| `LOG_FORMAT` | `json` | Log output format: `json` or `text` |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

### Other

| Variable | Default | Description |
|---|---|---|
| `TZ` | _(system)_ | Timezone for log timestamps (e.g. `Europe/Oslo`, `America/New_York`) |

---

## Settings Managed via UI

The following are **not** environment variables. They are configured through the dashboard **Settings** page and stored encrypted in the database:

| Setting | Where to configure |
|---|---|
| Repositories (URL, branch, stacks directory, sync interval, auth) | Settings → Repositories |
| SSH keys | Settings → SSH Keys |
| Infisical token, environment, URL, project ID | Settings → Secrets |
| Dashboard auth token | Settings → General |

See [Database](database.md) for details on persistence and encryption.

---

## Precedence

For the `DASHBOARD_TOKEN` specifically, the resolution order is:

1. `DASHBOARD_TOKEN` environment variable (highest priority)
2. Token saved via the Settings UI (stored in database)
3. No auth (open access)

All other repository and credential settings are UI-only and have no env var equivalent.
```

---