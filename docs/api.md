# API Reference

The stackd HTTP API is served on the same port as the dashboard (default `8080`). All `/api/*` endpoints require a bearer token when `DASHBOARD_TOKEN` is set.

---

## Authentication

Include the token in the `Authorization` header:

```sh
curl -H "Authorization: Bearer $DASHBOARD_TOKEN" http://localhost:8080/api/status
```

For WebSocket connections, pass the token as a `?token=` query parameter (the browser WebSocket API cannot send custom headers):

```
ws://localhost:8080/api/exec/my-container?token=your-token
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
          "stackDir": "/var/lib/stackd/repos/dockers/stacks/plex",
          "lastApply": "2024-06-04T12:00:05Z",
          "status": "ok",
          "lastOutput": "",
          "lastError": "",
          "infisicalMode": "global",
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
| `name` | string | Repository name |
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
| `stackDir` | string | Absolute path to the stack directory inside the container |
| `lastApply` | RFC3339 | Timestamp of last `docker compose up` (reflects container start time when available) |
| `status` | string | `ok`, `error`, or `applying` |
| `lastOutput` | string | Stdout from last compose apply |
| `lastError` | string | Error message from last failed apply |
| `infisicalMode` | string | `""` (none), `"global"` (global token), or `"per-stack"` (infisical.toml) |
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

Triggers an immediate full sync (pull + apply all stacks) for the named repository. Subject to rate limiting.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

**Path parameter:** `repo` — the repository name as configured in Settings

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

### POST /api/stacks/{repo}/{stack}/apply

Force-applies a single stack: pulls the repo then runs `docker compose up -d` for that stack only. Used by the per-stack sync button in the dashboard detail panel.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

**Path parameters:** `repo` — repository name, `stack` — stack name

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/stacks/dockers/plex/apply
```

**Responses:**

| Status | Body | Meaning |
|---|---|---|
| `200 OK` | `{"status":"applying"}` | Apply started in background |
| `404 Not Found` | `stack not found` | Unknown repo/stack combination |
| `503 Service Unavailable` | `apply not configured` | Internal setup error |

---

### GET /api/stacks/{repo}/{stack}/compose

Returns the raw content of the `docker-compose.yml` (or `compose.yaml`) file for a stack.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

**Path parameters:** `repo` — repository name, `stack` — stack name

**Response content type:** `text/plain; charset=utf-8`

```sh
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/stacks/dockers/plex/compose
```

Returns `404 Not Found` if the stack or compose file does not exist.

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

### GET /api/exec/{container}

Opens an interactive shell session (docker exec PTY) in the named container via WebSocket. The frontend uses xterm.js to render the terminal.

**Auth required:** Yes — pass `?token=<token>` as a query parameter (WebSocket API cannot send headers)

**Protocol:** WebSocket upgrade (`ws://` or `wss://`)

**Path parameter:** `container` — the container name or ID

```
ws://localhost:8080/api/exec/plex?token=your-token
```

- Binary and text messages from the client are forwarded to the container's stdin
- Binary messages from the server are PTY output
- To resize the PTY, send a JSON text message: `{"type":"resize","cols":120,"rows":40}`
- Session timeout: 30 minutes
- Returns `503 Service Unavailable` if Docker is unavailable

---

### GET /api/activity

Streams live activity events (git pulls, stack applies) as Server-Sent Events. Used by the dashboard activity feed.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

**Response content type:** `text/event-stream`

Each event is a JSON-encoded `ActivityEvent`:

```
data: {"type":"pulling","repo":"dockers","msg":"Pulling dockers"}

data: {"type":"applying","repo":"dockers","stack":"plex","msg":"Applying plex"}

data: {"type":"done","repo":"dockers","stack":"plex","msg":"plex ready"}

```

**ActivityEvent fields:**

| Field | Type | Description |
|---|---|---|
| `type` | string | `pulling`, `applying`, `done`, or `error` |
| `repo` | string | Repository name |
| `stack` | string | Stack name (omitted for repo-level events) |
| `msg` | string | Human-readable message |

```sh
curl -H "Authorization: Bearer $TOKEN" \
     -H "Accept: text/event-stream" \
     http://localhost:8080/api/activity
```

---

### POST /api/containers/{container}/start

Starts a stopped container.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/containers/plex/start
```

**Response:** `{"ok": true}` on success, `{"error": "..."}` with a non-200 status on failure.

---

### POST /api/containers/{container}/stop

Stops a running container.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/containers/plex/stop
```

---

### POST /api/containers/{container}/restart

Restarts a container.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/containers/plex/restart
```

---

### GET /api/settings/system

Returns system information about the running stackd instance.

**Auth required:** Yes (when `DASHBOARD_TOKEN` is set)

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/settings/system
```

**Response:**

```json
{
  "version": "v1.2.3",
  "uptime": "2h15m30s",
  "cloneDir": "/var/lib/stackd/repos",
  "dbPath": "/data/stackd.db",
  "goVersion": "go1.22.3"
}
```

---

### Settings CRUD Endpoints

The following endpoints power the Settings UI. They are available for scripting or integration but the primary interface is the dashboard.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/settings/repos` | List all repositories |
| `POST` | `/api/settings/repos` | Add a repository |
| `GET` | `/api/settings/repos/{id}` | Get a repository by ID |
| `PUT` | `/api/settings/repos/{id}` | Update a repository |
| `DELETE` | `/api/settings/repos/{id}` | Delete a repository |
| `GET` | `/api/settings/ssh-keys` | List SSH keys (private key content is never returned) |
| `POST` | `/api/settings/ssh-keys` | Add an SSH key |
| `DELETE` | `/api/settings/ssh-keys/{id}` | Delete an SSH key |
| `GET` | `/api/settings/general` | Get general settings (Infisical config, dashboard token status) |
| `PUT` | `/api/settings/general` | Update general settings |

All settings endpoints require auth when `DASHBOARD_TOKEN` is set.

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
```

---