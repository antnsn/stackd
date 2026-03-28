<div align="center">
  <img src="assets/logo.svg" alt="stackd" width="400"/>
  <br/>
  <br/>
  <a href="https://github.com/antnsn/stackd/releases"><img src="https://img.shields.io/github/v/release/antnsn/stackd?style=flat-square&color=58a6ff" alt="Release"/></a>
  <a href="https://github.com/antnsn/stackd/pkgs/container/stackd"><img src="https://img.shields.io/badge/container-ghcr.io-3fb950?style=flat-square" alt="Container"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-8b949e?style=flat-square" alt="MIT License"/></a>
</div>

---

**stackd** is a lightweight GitOps daemon for Docker Compose. It watches your Git repositories, pulls changes, injects secrets via Infisical, and applies your stacks automatically. A built-in dashboard lets you monitor repos, stacks, and container logs in real time.

Think of it as ArgoCD for Docker Compose, without the YAML sprawl.

## Features

- **GitOps sync** — polls one or more Git repos and applies `docker compose up -d` on change
- **Infisical integration** — wraps deploys with `infisical run` to inject secrets; no `.env` files in Git
- **Live dashboard** — dark-mode UI with stack status, container health, and real-time log streaming
- **Pull-only mode** — sync without writing back to Git (recommended for most setups)
- **Manual sync** — trigger a sync instantly from the dashboard without waiting for the next poll
- **Multi-repo** — mount as many repos as you need; each gets its own sync loop and stack config

## Quick Start

```yaml
services:
  stackd:
    container_name: stackd
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - TZ=Europe/Oslo
      - PULL_ONLY=true
      - SSH_KEY_PATH=/root/.ssh/id_ed25519
      - SYNC_INTERVAL_SECONDS=60
      - STACKS_DIR_DOCKERS=/repos/dockers/linuxServer/stacks
      - DASHBOARD_ENABLED=true
      - DASHBOARD_PORT=8080
    volumes:
      - /path/to/.ssh:/root/.ssh
      - /path/to/repo/dockers:/repos/dockers
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
```

Open `http://localhost:8080` to see the dashboard.

## Dashboard

The dashboard gives you a live view of everything stackd manages:

- **Repos** — sync status, last SHA, last sync time, manual sync button
- **Stacks** — per-stack deploy status with container-level health indicators
- **Logs** — click any container to open a real-time log stream in a modal
- **System** — Infisical config, sync interval, version, uptime

## Configuration

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PULL_ONLY` | `false` | Only pull from remote; skip `git add`, `commit`, `push` |
| `SYNC_INTERVAL_SECONDS` | `60` | How often to poll repos |
| `SSH_KEY_PATH` | `/root/.ssh/id_rsa` | Path to SSH private key inside the container |
| `GIT_USER_NAME` | `githubSync` | Git `user.name` for automated commits |
| `GIT_USER_EMAIL` | `githubsync@localhost` | Git `user.email` for automated commits |
| `TZ` | _(none)_ | Timezone (e.g. `Europe/Oslo`) |
| `STACKS_DIR_<REPO>` | _(none)_ | Path to stacks directory for a repo (uppercase repo name) |
| `POST_SYNC_<REPO>` | _(none)_ | Shell command to run after a successful pull |
| `DASHBOARD_ENABLED` | `false` | Enable the web dashboard |
| `DASHBOARD_PORT` | `8080` | Port for the dashboard |
| `INFISICAL_ENABLED` | `false` | Enable Infisical secrets injection |
| `INFISICAL_TOKEN` | _(none)_ | Global Infisical service token |
| `INFISICAL_ENV` | `prod` | Infisical environment (e.g. `dev`, `staging`, `prod`) |
| `INFISICAL_URL` | _(none)_ | Self-hosted Infisical instance URL |
| `BRANCH_DEFAULT` | `main` | Default git branch for all repos |
| `BRANCH_<REPO>` | `BRANCH_DEFAULT` | Git branch for a specific repo (e.g. `BRANCH_HOMELAB=master`) |
| `REMOTE_<REPO>` | `origin` | Git remote for a specific repo (e.g. `REMOTE_HOMELAB=upstream`) |
| `STACKD_CONFIG` | _(none)_ | Path to optional `stackd.yaml` config file |
| `LOG_FORMAT` | `json` | Log output format: `json` or `text` |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

### Stacks Layout

The `STACKS_DIR_<REPO>` variable points to a directory of Docker Compose stacks. Each subdirectory is a stack:

```
/repos/dockers/linuxServer/stacks/
  plex/
    compose.yaml
  sonarr/
    compose.yaml
  radarr/
    docker-compose.yml
```

`REPO` is the uppercase name of the mount directory. For a repo mounted at `/repos/dockers`, set `STACKS_DIR_DOCKERS`.

### Configuration File (Optional)

Instead of managing many environment variables, you can use a `stackd.yaml` file.
Place it in `REPOS_DIR` (default: `/repos/stackd.yaml`) or point to it with `STACKD_CONFIG`.

Environment variables always take precedence over the config file.

```yaml
# stackd.yaml
pullOnly: false
syncIntervalSeconds: 60
gitUser:
  name: "githubSync"
  email: "githubsync@localhost"
repos:
  - name: homelab
    stacksDir: /stacks/homelab
    branch: master
    remote: origin
    infisicalEnv: prod
  - name: work
    stacksDir: /stacks/work
    branch: main
    remote: origin
    postSyncCmd: "echo synced"
```

### Multi-Repo Example

```yaml
services:
  stackd:
    container_name: stackd
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - PULL_ONLY=true
      - STACKS_DIR_DOCKERS=/repos/dockers/linuxServer/stacks
      - STACKS_DIR_HOMELAB=/repos/homelab/synology/stacks
      - SYNC_INTERVAL_SECONDS=30
      - DASHBOARD_ENABLED=true
    volumes:
      - /path/to/.ssh:/root/.ssh
      - /path/to/repo/dockers:/repos/dockers
      - /path/to/repo/homelab:/repos/homelab
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
```

## Infisical

When `INFISICAL_ENABLED=true`, stackd wraps `docker compose up -d` with `infisical run` to inject secrets as environment variables. No `.env` files needed in your repo.

### Auth Priority (per stack)

1. **Per-stack `infisical.toml`** in the stack directory
2. **Global `INFISICAL_TOKEN`** + `INFISICAL_ENV` env vars
3. If neither is present and Infisical is enabled: deploy proceeds without secrets injection (warning logged)

### Per-Stack Config

```
stacks/
  plex/
    compose.yaml
    infisical.toml   ← uses its own Infisical project/env
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

### With Infisical

```yaml
services:
  stackd:
    container_name: stackd
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - PULL_ONLY=true
      - STACKS_DIR_DOCKERS=/repos/dockers/linuxServer/stacks
      - INFISICAL_ENABLED=true
      - INFISICAL_TOKEN=st.v3.xxxxxxxxxxxx
      - INFISICAL_ENV=prod
      - INFISICAL_URL=https://infisical.example.com
      - DASHBOARD_ENABLED=true
    volumes:
      - /path/to/.ssh:/root/.ssh
      - /path/to/repo/dockers:/repos/dockers
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
```

## SSH Setup

Mount your SSH directory and set `SSH_KEY_PATH`:

```yaml
volumes:
  - /home/user/.ssh:/root/.ssh

environment:
  - SSH_KEY_PATH=/root/.ssh/id_ed25519
```

stackd writes `known_hosts` and an SSH `config` automatically. No manual fingerprint confirmation needed.

## Post-Sync Hook

Run a command after a successful pull, before stack applies:

```yaml
environment:
  - POST_SYNC_DOCKERS=docker compose -f /repos/dockers/extra/compose.yaml up -d
```

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /` | Dashboard UI |
| `GET /api/status` | JSON state snapshot |
| `POST /api/sync/{repo}` | Trigger immediate sync |
| `GET /api/logs/{container}` | SSE container log stream |
| `GET /healthz` | Liveness probe — always `200 OK` |
| `GET /readyz` | Readiness probe — `200` when ready, `503` otherwise |
| `GET /metrics` | Prometheus metrics |

## Logging

```sh
docker logs -f stackd
```

## License

MIT — see [LICENSE](LICENSE).
