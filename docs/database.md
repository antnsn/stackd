# Database

stackd uses **SQLite** as its configuration store. It holds repositories, SSH keys, and settings (including encrypted secrets). It is separate from the in-memory state store, which tracks live stack and container status and is rebuilt on every restart.

---

## SQLite (default)

stackd creates and manages its own SQLite database automatically. No external database setup is required.

### Persistence

The database **must be persisted via a volume mount** or all configuration (repos, SSH keys, Infisical tokens) will be lost on container restart:

```yaml
services:
  stackd:
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - SECRET_KEY=change-me-use-openssl-rand-hex-32
      - DB_URL=sqlite:///data/stackd.db
    volumes:
      - /path/to/stackd-data:/data          # persists stackd.db
      - /var/run/docker.sock:/var/run/docker.sock
```

The default database path is `stackd.db` in the working directory. Set `DB_URL` to an absolute path inside a mounted volume to make it explicit:

```yaml
DB_URL=sqlite:///data/stackd.db
```

### SECRET_KEY

`SECRET_KEY` is **required**. It is used to encrypt sensitive values (Infisical tokens, SSH keys) before they are written to the database. stackd will refuse to start if this variable is missing.

Generate a strong key:

```sh
openssl rand -hex 32
```

> **Keep this key backed up separately.** If you lose `SECRET_KEY`, the encrypted values in the database are unrecoverable. The key must remain the same across restarts — changing it invalidates all stored secrets.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `SECRET_KEY` | _(required)_ | Encryption key for secrets stored in the database |
| `DB_URL` | `sqlite://stackd.db` | SQLite file path. Use `sqlite:///data/stackd.db` with a `/data` volume |

---

## What's Stored

| Table | Contents |
|---|---|
| `repos` | Repository URLs, branch, sync interval, stacks directory |
| `ssh_keys` | SSH private keys (stored encrypted with `SECRET_KEY`) |
| `settings` | Key/value config: Infisical token (encrypted), Infisical env, URL, project ID, dashboard token |

Settings are managed through the dashboard **Settings UI**. Do not edit the database directly.

---

## PostgreSQL (alternative)

stackd supports PostgreSQL as an alternative to SQLite for environments where a shared or externally managed database is preferred.

Set `DB_URL` to a PostgreSQL connection string:

```yaml
environment:
  - SECRET_KEY=change-me-use-openssl-rand-hex-32
  - DB_URL=postgres://user:password@host:5432/stackd?sslmode=disable
```

> **Note:** The PostgreSQL database in the example below is a **workload that stackd manages** — it is not stackd's own backend. stackd deploys it via `docker compose up -d`, the same as any other stack.
>
> ```yaml
> # This is a stack stackd DEPLOYS, not stackd's own database
> services:
>   db:
>     image: postgres:14.12-alpine
>     environment:
>       POSTGRES_USER: ${POSTGRES_USER}
>       POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
>       POSTGRES_DB: ${POSTGRES_DB}
> ```
>
> If you want stackd itself to use Postgres, point `DB_URL` at a separate Postgres instance — not the one stackd is managing.

---

## Backup

For SQLite, back up the database file while stackd is stopped, or use SQLite's online backup:

```sh
sqlite3 /path/to/stackd-data/stackd.db ".backup '/path/to/backup/stackd.db'"
```

The backup contains encrypted values — keep `SECRET_KEY` backed up separately.
