# Multi-Repo Setup

stackd manages multiple git repositories simultaneously. Each repository has its own sync loop, stacks directory, branch, auth credentials, and sync interval — all configured through the Settings UI.

---

## Adding Multiple Repositories

Repositories are added through the dashboard **Settings → Repositories** page. There is no limit on the number of repositories. Each one is stored in the database and syncs independently.

For each repository you configure:

| Setting | Description |
|---|---|
| **Name** | Display name — used in the dashboard, API paths (`/api/sync/{name}`), logs, and metrics |
| **URL** | Git remote URL — SSH (`git@github.com:org/repo.git`) or HTTPS |
| **Branch** | Branch to track (default: `main`) |
| **Stacks directory** | Path relative to the repo root to the folder containing stack subdirectories (e.g. `stacks`, `docker/stacks`) |
| **Sync interval** | How often to poll for changes, in seconds (default: 60) |
| **Auth method** | `ssh` (select a key from Settings → SSH Keys), `pat` (personal access token), or none |

---

## Stack Discovery

stackd discovers stacks by scanning the configured stacks directory for subdirectories that contain a compose file (`compose.yaml`, `docker-compose.yml`, `compose.yml`, `docker-compose.yaml`):

```
stacks/
  plex/
    docker-compose.yml    ← discovered as stack "plex"
  jellyfin/
    compose.yaml          ← discovered as stack "jellyfin"
  README.md               ← ignored (not a directory)
  shared/                 ← skipped (no compose file inside)
```

---

## How Independent Sync Loops Work

Each repo syncs on its own configured interval. Key properties:

- **Independent backoff** — one repo failing does not affect others
- **Independent rate limiting** — manual sync rate limits are tracked per repo
- **Independent state** — each repo has its own `RepoState` entry in the state store
- **Shared Docker client** — all repos share the same Docker daemon connection
- **Shared dashboard** — all repos appear on the same dashboard and in the same `/api/status` response

---

## Per-Stack Sync

In addition to the repo-level sync (`POST /api/sync/{repo}`), you can trigger a sync for a single stack from the dashboard detail panel or via the API:

```sh
# Pull the repo and apply only the "plex" stack
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/stacks/dockers/plex/apply
```

---

## Monitoring Multiple Repos

The `/api/status` endpoint returns all repos and their stacks in a single response. Use the activity feed (`GET /api/activity`) for a live stream of events across all repos:

```sh
curl -H "Authorization: Bearer $TOKEN" \
     -H "Accept: text/event-stream" \
     http://localhost:8080/api/activity
```

See [API Reference](api.md) for full details on both endpoints.

---

## Prometheus Metrics

All metrics include a `repo` label, making it easy to monitor each repository independently:

```
stackd_sync_total{repo="dockers",status="success"} 42
stackd_sync_total{repo="homelab",status="success"} 38
stackd_last_sync_timestamp{repo="dockers"} 1717500000
stackd_last_sync_timestamp{repo="homelab"} 1717499950
```

See [Observability](observability.md) for the full metrics reference.
```

---