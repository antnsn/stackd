# Infisical Secrets Integration

stackd integrates with [Infisical](https://infisical.com) to inject secrets as environment variables at deploy time. When configured, `docker compose up -d` is wrapped with `infisical run --`, so your compose files never need `.env` files committed to Git.

> **Configuration is done through the dashboard Settings UI**, not environment variables. Infisical credentials are stored encrypted in the stackd database.

---

## How It Works

For each stack apply, stackd checks in this order:

1. **Per-stack `infisical.toml`** — if an `infisical.toml` exists in the stack directory, it is used via `infisical run --config=<path> -- docker compose up -d`
2. **Global token** — if no `infisical.toml` is found but a global token is configured in Settings, it is used via `infisical run --token=<TOKEN> --env=<ENV> -- docker compose up -d`
3. **No credentials** — plain `docker compose up -d` with no secrets injection

The dashboard shows which mode each stack is using: stacks with active Infisical injection display a **🔑 key icon** on their card and a **Secrets row** in the detail panel showing `global` or `per-stack`.

---

## Migrating a Compose File to Use Infisical

Replace hardcoded secret values with `${VAR_NAME}` Docker Compose variable substitution. Compose reads `${}` from the process environment — which Infisical populates via `infisical run --`.

**Before:**

```yaml
services:
  db:
    image: postgres:14.12-alpine
    container_name: db
    restart: always
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: postgres
```

**After:**

```yaml
services:
  db:
    image: postgres:14.12-alpine
    container_name: db
    restart: always
    environment:
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB}
```

Then create secrets named `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_DB` in your Infisical project with the actual values.

> **Note:** Stacks with no `${}` or `$VAR` references will not show the key icon even if a global token is configured — stackd detects whether the compose file actually uses variable substitution.

---

## Global Token Setup

1. Open the stackd dashboard → **Settings**
2. Under **Secrets**, enter your Infisical machine token and select the environment (`prod`, `dev`, `staging`, etc.)
3. Optionally enter a **Project ID** to scope secrets to a specific Infisical project
4. **Save** — the token is encrypted and stored in the stackd database

All stacks whose compose files contain `${}` variable references will automatically use this token on the next apply.

---

## Per-Stack infisical.toml

Place an `infisical.toml` file in a stack directory to give that stack its own Infisical configuration — useful for different projects or environments per stack:

```
stacks/
  postgres/
    docker-compose.yml
    infisical.toml      ← own Infisical project + environment
  jellyfin/
    docker-compose.yml  ← no secrets, no infisical.toml
  sonarr/
    docker-compose.yml  ← uses global token if compose has ${VAR}
```

A minimal `infisical.toml`:

```toml
[infisical]
address = "https://infisical.example.com"

[auth]
strategy = "token"

[project]
project_id = "your-project-uuid"
default_environment = "prod"
```

The `infisical.toml` approach is always used when present, regardless of whether the compose file contains variable references.

---

## Self-Hosted Infisical

In Settings, set the **Infisical URL** field to point at your own instance (e.g. `https://infisical.internal.example.com`). This is passed to the Infisical CLI as `--domain=<url>` for every stack apply.

---

## Secrets Masking in Logs

stackd redacts sensitive values before writing to structured logs. Any env var whose name contains the following substrings (case-insensitive) is replaced with `[redacted]`:

`TOKEN` · `SECRET` · `KEY` · `PASSWORD` · `PASS` · `CREDENTIAL`

This means `POSTGRES_PASSWORD`, Infisical tokens, and SSH key values will never appear in plaintext log output.
