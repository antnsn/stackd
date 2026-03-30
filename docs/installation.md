# Installation

## Prerequisites

- **Docker ≥ 20.10** with the **Docker Compose plugin** (`docker compose`, not `docker-compose`)
- **Docker socket** access — stackd calls the Docker daemon to manage stacks
- A strong random value for `SECRET_KEY` — generate one with `openssl rand -hex 32`

---

## Minimal Setup

Create a `docker-compose.yml` on your host:

```yaml
services:
  stackd:
    container_name: stackd
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - SECRET_KEY=change-me-use-openssl-rand-hex-32
      - DB_URL=sqlite:///data/stackd.db
      - PORT=8080
    volumes:
      - /path/to/stackd-data:/data
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

Start stackd:

```sh
docker compose up -d
```

Open `http://localhost:8080` and use the **Settings → Repositories** page to add your first repository.

---

## Adding Your First Repository

Repositories are configured through the Settings UI — not environment variables.

1. Open the dashboard → **Settings → Repositories → Add Repository**
2. Enter the repository URL:
   - SSH (private repos): `git@github.com:org/repo.git`
   - HTTPS (public repos): `https://github.com/org/repo.git`
3. Set the **branch** (default: `main`), **stacks directory** (path within the repo to the folder containing stack subdirectories), and **sync interval**
4. Choose an **auth method**:
   - **SSH key** — select a key you've added under Settings → SSH Keys
   - **Personal Access Token (PAT)** — enter a token (stored encrypted)
   - **None** — for public repositories
5. **Save** — stackd will immediately attempt a sync

---

## Volume Mount Reference

| Host Path | Container Path | Why |
|---|---|---|
| `/path/to/stackd-data` | `/data` | Persists `stackd.db` — contains all repos, SSH keys, and settings |
| `/var/run/docker.sock` | `/var/run/docker.sock` | Docker socket so stackd can run `docker compose` |

> **Tip:** The database volume is the only required persistent mount. stackd clones repositories internally into `CLONE_DIR` (default: `/var/lib/stackd/repos`) — you do not need to pre-clone or mount repo directories.

---

## First-Run Checklist

1. **`SECRET_KEY` is set** — required; stackd exits immediately without it
2. **`DB_URL` points into a mounted volume** — otherwise configuration is lost on container restart
3. **Docker socket is mounted** — `docker compose` cannot run without it
4. **Dashboard port is published** — add `ports: ["8080:8080"]`
5. **At least one repository is configured** — use Settings → Repositories after first start

---

## Multi-Repo Setup

Add as many repositories as you need through the Settings UI. Each repository syncs independently with its own interval and stacks directory. See [Multi-Repo Setup](multi-repo.md) for patterns and tips.

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
```

---