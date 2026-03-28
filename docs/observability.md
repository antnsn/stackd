# Observability

---

## Logging

### Format

stackd writes structured logs to stdout. The default format is JSON; switch to human-readable text with `LOG_FORMAT=text`:

```yaml
environment:
  - LOG_FORMAT=text   # json (default) or text
  - LOG_LEVEL=info    # debug, info, warn, error
```

### Log Levels

| Level | When to use |
|---|---|
| `debug` | Verbose output including every git operation and compose command |
| `info` | Normal operational events (syncs, applies, server start) |
| `warn` | Recoverable issues (backoff, missing credentials, skipped stacks) |
| `error` | Failures that need attention |

### Structured Fields

JSON log entries include contextual fields depending on the operation:

| Field | Description |
|---|---|
| `repo` | Repository name |
| `stack` | Stack name |
| `err` | Error message (present on warn/error) |
| `sha` | Git commit SHA after a successful pull |
| `backoff` | Backoff duration (on sync failures) |
| `failures` | Consecutive failure count |

### Viewing Logs

```sh
docker logs -f stackd
```

With `LOG_FORMAT=text` and `LOG_LEVEL=debug`:

```sh
docker logs -f stackd 2>&1 | grep -v "^{"
```

---

## Health Probes

### GET /healthz — Liveness

Always returns `200 OK`. Use this to tell your orchestrator that the process is alive.

```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
```

### GET /readyz — Readiness

Returns `200 OK` once both conditions are met:
- Docker client is connected
- At least one stack has been applied at least once

Returns `503 Service Unavailable` until then:

```json
{
  "status": "not_ready",
  "docker": true,
  "synced": false
}
```

### Docker Compose Healthcheck

Add a healthcheck to your `docker-compose.yml` to integrate with Docker's restart policies:

```yaml
services:
  stackd:
    image: ghcr.io/antnsn/stackd:latest
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

---

## Prometheus Metrics

stackd exposes Prometheus-format metrics at `GET /metrics` (no auth required).

### Metrics Reference

| Metric | Type | Labels | Description |
|---|---|---|---|
| `stackd_sync_total` | Counter | `repo`, `status` | Total git sync attempts, labelled by repo and outcome (`success`, `error`, `skipped`) |
| `stackd_sync_duration_seconds_sum` | Gauge | `repo` | Cumulative seconds spent on successful syncs for a repo |
| `stackd_sync_duration_seconds_count` | Gauge | `repo` | Number of successful syncs counted in the duration sum |
| `stackd_stack_apply_total` | Counter | `stack`, `status` | Total `docker compose up` calls, labelled by stack and outcome (`ok`, `error`) |
| `stackd_containers_running` | Gauge | `stack` | Number of running containers for a stack at last refresh |
| `stackd_last_sync_timestamp` | Gauge | `repo` | Unix timestamp of the last successful sync for a repo |

### Sample Output

```
stackd_sync_total{repo="dockers",status="success"} 42
stackd_sync_total{repo="dockers",status="error"} 1
stackd_sync_duration_seconds_sum{repo="dockers"} 18.3421
stackd_sync_duration_seconds_count{repo="dockers"} 42
stackd_stack_apply_total{stack="dockers/plex",status="ok"} 12
stackd_containers_running{stack="dockers/plex"} 3
stackd_last_sync_timestamp{repo="dockers"} 1717500000
```

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: stackd
    static_configs:
      - targets: ["stackd-host:8080"]
```

### Grafana Dashboard Hints

Suggested panels for a Grafana dashboard:

- **Sync rate** — `rate(stackd_sync_total{status="success"}[5m])` — syncs per second
- **Sync error rate** — `rate(stackd_sync_total{status="error"}[5m])` — errors per second
- **Average sync duration** — `stackd_sync_duration_seconds_sum / stackd_sync_duration_seconds_count`
- **Containers running** — `stackd_containers_running` — gauge panel per stack
- **Last sync age** — `time() - stackd_last_sync_timestamp` — how stale each repo is (alert if > 2× interval)
- **Apply failures** — `increase(stackd_stack_apply_total{status="error"}[1h])` — failed applies in the last hour
