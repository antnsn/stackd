# API Reference

The stackd HTTP API is served on the same port as the dashboard (default `8080`). All `/api/*` endpoints require a bearer token when `DASHBOARD_TOKEN` is set.

---

## Authentication

Include the token in the `Authorization` header:

```sh
curl -H "Authorization: Bearer $DASHBOARD_TOKEN" http://localhost:8080/api/status
```

Unauthenticated requests to protected endpoints return `401 Unauthorized`.

---

## Endpoints

### GET /api/status

Returns the full current state as JSON: all repos, their stacks, and the global Infisical config.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/status
```

**Response schema:**

```json
{
  "repos": [
    {
      "name": "dockers",
      "lastSync": "2024-06-04T12:00:00Z",
      "lastSha": "a1b2c3d4e5f6...",
      "status": "ok",
      "lastError": "",
      "infisical": {
        "enabled": true,
        "env": "prod"
      },
      "stacks": [
        {
          "name": "plex",
          "repoName": "dockers",
          "stackDir": "/repos/dockers/linuxServer/stacks/plex",
          "lastApply": "2024-06-04T12:00:05Z",
          "status": "ok",
          "lastOutput": "",
          "lastError": "",
          "containers": [
            {
              "id": "abc123def456",
              "name": "plex",
              "image": "plexinc/pms-docker:latest",
              "status": "running",
              "startedAt": "2024-06-04T12:00:06Z",
              "env": ["PLEX_CLAIM=[redacted]", "TZ=Europe/Oslo"],
              "ports": ["32400:32400/tcp"]
            }
          ]
        }
      ]
    }
  ],
  "infisical": {
    "enabled": true,
    "env": "prod"
  }
}
```

**RepoState fields:**

| Field | Type | Description |
|---|---|---|
| `name` | string | Repository name (directory basename) |
| `lastSync` | RFC3339 | Timestamp of last sync attempt |
| `lastSha` | string | Git SHA after last successful pull |
| `status` | string | `ok`, `error`, or `syncing` |
| `lastError` | string | Error message from last failed sync (omitted if empty) |
| `infisical` | object | Global Infisical state (enabled flag + environment name) |
| `stacks` | array | Stack states for this repo |

**StackState fields:**

| Field | Type | Description |
|---|---|---|
| `name` | string | Stack name (subdirectory name under `stacksDir`) |
| `repoName` | string | Parent repository name |
| `stackDir` | string | Absolute path to the stack directory |
| `lastApply` | RFC3339 | Timestamp of last `docker compose up` |
| `status` | string | `ok`, `error`, or `applying` |
| `lastOutput` | string | Stdout from last compose apply |
| `lastError` | string | Error message from last failed apply |
| `containers` | array | Container details |

**ContainerDetail fields:**

| Field | Type | Description |
|---|---|---|
| `id` | string | Container ID (short) |
| `name` | string | Container name |
| `image` | string | Image name and tag |
| `status` | string | Docker container status (e.g. `running`, `exited`) |
| `startedAt` | RFC3339 | When the container last started |
| `env` | array | Environment variables (sensitive values masked as `[redacted]`) |
| `ports` | array | Published ports in `"host:container/proto"` format |

---

### POST /api/sync/{repo}

Triggers an immediate sync for the named repository, bypassing the polling interval. Subject to rate limiting.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

**Path parameter:** `repo` — the repository name (must match a directory under `REPOS_DIR`)

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/sync/dockers
```

**Responses:**

| Status | Body | Meaning |
|---|---|---|
| `200 OK` | `{"status":"queued","repo":"dockers"}` | Sync enqueued successfully |
| `202 Accepted` | `{"status":"already_queued","repo":"dockers"}` | A sync was already pending |
| `401 Unauthorized` | `Unauthorized` | Missing or invalid token |
| `429 Too Many Requests` | `{"status":"rate_limited","repo":"dockers"}` | Rate limit exceeded (see `SYNC_RATE_LIMIT_SECONDS`) |

---

### GET /api/logs/{container}

Streams container logs as [Server-Sent Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events) (SSE).

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

**Path parameter:** `container` — the container name

**Response content type:** `text/event-stream`

Each log line is sent as an SSE event:

```
data: 2024-06-04T12:00:07Z | Server started on port 32400

data: 2024-06-04T12:00:08Z | Loaded library from /data/Media

```

```sh
curl -H "Authorization: Bearer $TOKEN" \
     -H "Accept: text/event-stream" \
     http://localhost:8080/api/logs/plex
```

The stream stays open until the client disconnects or the container stops. Returns `503 Service Unavailable` if the Docker client is unavailable.

---

### GET /healthz

Liveness probe. Always returns `200 OK` as long as the process is running. No auth required.

```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
```

---

### GET /readyz

Readiness probe. Returns `200 OK` once Docker is connected and at least one stack has been applied. No auth required.

```sh
curl http://localhost:8080/readyz
# {"status":"ready"}
```

Returns `503 Service Unavailable` with a diagnostic body if not ready:

```json
{"status": "not_ready", "docker": true, "synced": false}
```

---

### GET /metrics

Prometheus metrics in text exposition format. No auth required.

```sh
curl http://localhost:8080/metrics
```

See [Observability](observability.md#prometheus-metrics) for the full metrics reference.
