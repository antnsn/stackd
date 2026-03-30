<div align="center">
  <img src="assets/logo.svg" alt="stackd" width="400"/>
  <br/>
  <br/>
  <a href="https://github.com/antnsn/stackd/releases"><img src="https://img.shields.io/github/v/release/antnsn/stackd?style=flat-square&color=58a6ff" alt="Release"/></a>
  <a href="https://github.com/antnsn/stackd/pkgs/container/stackd"><img src="https://img.shields.io/badge/container-ghcr.io-3fb950?style=flat-square" alt="Container"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-8b949e?style=flat-square" alt="AGPL-3.0 License"/></a>
  <a href="COMMERCIAL.md"><img src="https://img.shields.io/badge/commercial-licensing-f78166?style=flat-square" alt="Commercial Licensing"/></a>
</div>

---

**stackd** is a lightweight GitOps daemon for Docker Compose. It watches your Git repositories, pulls changes, injects secrets via Infisical, and applies your stacks automatically. A built-in dashboard lets you monitor repos, stacks, and container logs in real time.

Think of it as ArgoCD for Docker Compose, without the YAML sprawl.

## Features

- **GitOps sync** — polls one or more Git repos and applies `docker compose up -d` on change
- **Infisical integration** — wraps deploys with `infisical run` to inject secrets; no `.env` files in Git
- **Live dashboard** — dark-mode UI with stack status, container health, and real-time log streaming
- **Web shell** — browser-based terminal (docker exec PTY) for any running container via xterm.js
- **Activity feed** — live SSE stream of git pulls and stack applies across all repos
- **Per-stack sync** — trigger a pull + apply for a single stack from the detail panel
- **Settings UI** — manage repos, SSH keys, Infisical credentials, and auth token from the dashboard
- **Multi-repo** — manage as many repos as you need; each gets its own sync loop and stack config

## Quick Start

> **Note:** `SECRET_KEY` is required. stackd will refuse to start without it — it encrypts SSH keys and tokens stored in the database.

```yaml
services:
  stackd:
    container_name: stackd
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - SECRET_KEY=change-me-use-a-strong-random-value
      - DB_URL=sqlite:///data/stackd.db
      - PORT=8080
    volumes:
      - /path/to/stackd-data:/data
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
    restart: unless-stopped
```

Generate a strong `SECRET_KEY`:

```sh
openssl rand -hex 32
```

Then:

```sh
docker compose up -d
```

Open `http://localhost:8080` and use the **Settings** page to add your first repository.

## Documentation

| | |
|---|---|
| [Configuration](docs/configuration.md) | All env vars and their defaults |
| [Installation](docs/installation.md) | Docker Compose setup, volume mounts, first run |
| [SSH Setup](docs/ssh.md) | SSH key configuration via Settings UI and troubleshooting |
| [Infisical Secrets](docs/infisical.md) | Secrets injection — global token, per-stack toml |
| [Database](docs/database.md) | SQLite config store, SECRET_KEY, persistence, PostgreSQL option |
| [Security](docs/security.md) | Auth, rate limiting, production hardening |
| [Observability](docs/observability.md) | Logging, health probes, Prometheus metrics |
| [API Reference](docs/api.md) | Full HTTP API with request/response examples |
| [Multi-Repo Setup](docs/multi-repo.md) | Managing multiple repositories |
| [Post-Sync Hooks](docs/post-sync-hooks.md) | Alternatives now that env-var hooks are removed |
| [Architecture](docs/architecture.md) | How stackd works internally |

## License

AGPL-3.0 — see [LICENSE](LICENSE). For commercial licensing, see [COMMERCIAL.md](COMMERCIAL.md).
```

---