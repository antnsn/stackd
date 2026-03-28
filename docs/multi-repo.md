# Multi-Repo Setup

stackd is designed to manage multiple git repositories simultaneously. Each repository runs its own independent sync loop, has its own stack configuration, and can have its own branch, remote, and Infisical settings.

---

## Concept

Mount one directory per repository under `REPOS_DIR` (default: `/repos`). stackd discovers all subdirectories at startup and treats each one as an independent repo.

```
/repos/
  dockers/       ← repo 1 (git clone of git@github.com:you/dockers.git)
  homelab/       ← repo 2 (git clone of git@github.com:you/homelab.git)
  work/          ← repo 3 (git clone of git@github.com:you/work.git)
```

The **repo name** is always the basename of the directory — `dockers`, `homelab`, `work`. This name is used in:
- Per-repo env var suffixes (`STACKS_DIR_DOCKERS`, `BRANCH_HOMELAB`, etc.) — always uppercase
- API paths (`POST /api/sync/dockers`)
- Log fields and metrics labels

---

## Per-Repo Environment Variables

For each repo, you can set any of these env vars by appending the uppercase repo name:

| Pattern | Example | Description |
|---|---|---|
| `STACKS_DIR_<REPO>` | `STACKS_DIR_DOCKERS=/repos/dockers/stacks` | Path to stacks directory |
| `BRANCH_<REPO>` | `BRANCH_HOMELAB=master` | Git branch to track |
| `REMOTE_<REPO>` | `REMOTE_WORK=upstream` | Git remote name |
| `POST_SYNC_<REPO>` | `POST_SYNC_DOCKERS=echo synced` | Shell command after pull |

---

## Full Multi-Repo docker-compose.yml Example

```yaml
services:
  stackd:
    container_name: stackd
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - PULL_ONLY=true
      - SYNC_INTERVAL_SECONDS=60
      - DASHBOARD_ENABLED=true
      - DASHBOARD_PORT=8080
      - DASHBOARD_TOKEN=your-secret-token

      # Repo 1: dockers
      - STACKS_DIR_DOCKERS=/repos/dockers/linuxServer/stacks
      - BRANCH_DOCKERS=main

      # Repo 2: homelab
      - STACKS_DIR_HOMELAB=/repos/homelab/synology/stacks
      - BRANCH_HOMELAB=master

      # Repo 3: work
      - STACKS_DIR_WORK=/repos/work/docker/stacks
      - BRANCH_WORK=develop
      - POST_SYNC_WORK=curl -s https://hooks.example.com/work-deployed

    volumes:
      - /home/user/.ssh:/root/.ssh:ro
      - /path/to/dockers:/repos/dockers
      - /path/to/homelab:/repos/homelab
      - /path/to/work:/repos/work
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
    restart: unless-stopped
```

---

## Using stackd.yaml Instead

For complex multi-repo setups, a `stackd.yaml` config file is cleaner than many env vars:

```yaml
# /repos/stackd.yaml
pullOnly: true
syncIntervalSeconds: 60

repos:
  - name: dockers
    stacksDir: /repos/dockers/linuxServer/stacks
    branch: main

  - name: homelab
    stacksDir: /repos/homelab/synology/stacks
    branch: master

  - name: work
    stacksDir: /repos/work/docker/stacks
    branch: develop
    postSyncCmd: "curl -s https://hooks.example.com/work-deployed"
```

Mount the file and point stackd to it:

```yaml
volumes:
  - /path/to/stackd.yaml:/repos/stackd.yaml
environment:
  - STACKD_CONFIG=/repos/stackd.yaml
```

See [Configuration](configuration.md#stackdyaml-configuration-file) for the full schema.

---

## How Independent Sync Loops Work

Each repo gets its own goroutine in the main sync loop. They run concurrently, triggered by a shared ticker. Key properties:

- **Independent backoff** — one repo failing does not affect others
- **Independent rate limiting** — manual sync rate limits are tracked per repo
- **Independent state** — each repo has its own `RepoState` entry in the state store
- **Shared Docker client** — all repos share the same Docker daemon connection for `docker compose` calls
- **Shared dashboard** — all repos appear on the same dashboard and in the same `/api/status` response
