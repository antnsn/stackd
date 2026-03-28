# Installation

## Prerequisites

- **Docker ≥ 20.10** with the **Docker Compose plugin** (`docker compose`, not `docker-compose`)
- **SSH key** — required for cloning private repositories over SSH (Ed25519 recommended)
- **Docker socket** access — stackd calls the Docker daemon to manage stacks

---

## Minimal Single-Repo Setup

The quickest way to get started is with a single repository. Create a `docker-compose.yml` on your host:

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
      - /home/user/.ssh:/root/.ssh:ro
      - /path/to/repo/dockers:/repos/dockers
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
    restart: unless-stopped
```

Then run:

```sh
docker compose up -d
```

Open `http://localhost:8080` to see the dashboard.

---

## Volume Mount Reference

| Host Path | Container Path | Why |
|---|---|---|
| `/home/user/.ssh` | `/root/.ssh` | SSH key for cloning private git repositories |
| `/path/to/repo/myrepo` | `/repos/myrepo` | The git repository stackd will manage |
| `/var/run/docker.sock` | `/var/run/docker.sock` | Docker socket so stackd can run `docker compose` |
| _(optional)_ `/path/to/stackd.yaml` | `/repos/stackd.yaml` | Optional config file (or set `STACKD_CONFIG`) |

> **Tip:** Mount the SSH directory read-only (`:ro`) — stackd only reads the key, never writes to it.

---

## First-Run Checklist

1. **SSH key exists and is readable** — verify with `ls -la /home/user/.ssh/id_ed25519`
2. **Repository is accessible** — test SSH access with `ssh -T git@github.com` before starting stackd
3. **`STACKS_DIR_<REPO>` is set** — the value must be a path that exists inside the mounted repo volume
4. **Docker socket is mounted** — `docker compose` cannot run without it
5. **Dashboard port is published** — add `ports: ["8080:8080"]` and set `DASHBOARD_ENABLED=true`

---

## Upgrading

stackd follows a rolling release model. To upgrade to the latest version:

```sh
docker compose pull stackd
docker compose up -d stackd
```

To pin to a specific release, replace `latest` with a version tag:

```yaml
image: ghcr.io/antnsn/stackd:v1.2.3
```

Available tags are listed at [ghcr.io/antnsn/stackd](https://github.com/antnsn/stackd/pkgs/container/stackd).
