# Infisical Secrets Integration

stackd integrates with [Infisical](https://infisical.com) to inject secrets as environment variables at deploy time. When enabled, `docker compose up -d` is wrapped with `infisical run --`, so your compose files never need `.env` files committed to git.

---

## Enabling Infisical

Set `INFISICAL_ENABLED=true` in your environment:

```yaml
environment:
  - INFISICAL_ENABLED=true
  - INFISICAL_TOKEN=st.v3.xxxxxxxxxxxx
  - INFISICAL_ENV=prod
```

When enabled, every stack apply becomes:

```sh
infisical run [--token=... | --config=infisical.toml] --env=prod -- docker compose up -d
```

---

## Auth Priority

For each stack, stackd checks credentials in this order:

1. **Per-stack `infisical.toml`** — if an `infisical.toml` file exists in the stack directory, it is passed to Infisical via `--config=<path>`. This allows different stacks to use different Infisical projects or environments.
2. **Global `INFISICAL_TOKEN`** — if no `infisical.toml` is found but `INFISICAL_TOKEN` is set, the global token and `INFISICAL_ENV` are used.
3. **No credentials** — if neither is present and `INFISICAL_ENABLED=true`, a warning is logged and the stack is applied **without** secrets injection (the deploy still proceeds).

---

## Per-Stack infisical.toml

Place an `infisical.toml` file in any stack directory to give that stack its own Infisical configuration:

```
stacks/
  plex/
    compose.yaml
    infisical.toml   ← uses its own Infisical project/environment
  sonarr/
    compose.yaml     ← falls back to global INFISICAL_TOKEN
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

---

## Global Token Setup

For stacks that don't have their own `infisical.toml`, set the global token and environment:

```yaml
environment:
  - INFISICAL_ENABLED=true
  - INFISICAL_TOKEN=st.v3.xxxxxxxxxxxx   # service token from Infisical dashboard
  - INFISICAL_ENV=prod                    # environment: dev, staging, prod, etc.
```

---

## Self-Hosted Infisical

Point stackd at your own Infisical instance with `INFISICAL_URL`:

```yaml
environment:
  - INFISICAL_ENABLED=true
  - INFISICAL_TOKEN=st.v3.xxxxxxxxxxxx
  - INFISICAL_ENV=prod
  - INFISICAL_URL=https://infisical.internal.example.com
```

When `INFISICAL_URL` is set, it is passed to the Infisical CLI as `--domain=<url>` for every stack apply.

---

## Directory Layout Example

```
stacks/
  plex/
    compose.yaml
    infisical.toml    ← own project, own environment
  sonarr/
    compose.yaml      ← uses global INFISICAL_TOKEN + INFISICAL_ENV
  radarr/
    compose.yaml      ← uses global INFISICAL_TOKEN + INFISICAL_ENV
  homeassistant/
    compose.yaml
    infisical.toml    ← own project, different environment
```

---

## Secrets Masking in Logs

stackd automatically redacts sensitive environment variable values before writing them to structured logs. Any env var whose name contains one of the following substrings (case-insensitive) is replaced with `[redacted]`:

- `TOKEN`
- `SECRET`
- `KEY`
- `PASSWORD`
- `PASS`
- `CREDENTIAL`

This means `INFISICAL_TOKEN`, `DASHBOARD_TOKEN`, and similar variables will never appear in plaintext in log output.
