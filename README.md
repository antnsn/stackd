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

## Documentation

| | |
|---|---|
| [Configuration](docs/configuration.md) | All env vars, stackd.yaml schema, per-repo overrides |
| [Installation](docs/installation.md) | Docker Compose setup, volume mounts, first run |
| [SSH Setup](docs/ssh.md) | SSH key configuration and troubleshooting |
| [Infisical Secrets](docs/infisical.md) | Secrets injection integration |
| [Security](docs/security.md) | Auth, rate limiting, production hardening |
| [Observability](docs/observability.md) | Logging, health probes, Prometheus metrics |
| [API Reference](docs/api.md) | Full HTTP API with request/response examples |
| [Multi-Repo Setup](docs/multi-repo.md) | Managing multiple repositories |
| [Post-Sync Hooks](docs/post-sync-hooks.md) | Run commands after a successful sync |
| [Architecture](docs/architecture.md) | How stackd works internally |

## License

MIT — see [LICENSE](LICENSE).
