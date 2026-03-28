# Security

---

## Dashboard Authentication

By default, the dashboard API is open. Set `DASHBOARD_TOKEN` to require a bearer token on all `/api/*` endpoints:

```yaml
environment:
  - DASHBOARD_TOKEN=your-secret-token
```

The dashboard UI sends the token automatically. When calling the API directly, include it in the `Authorization` header:

```sh
curl -H "Authorization: Bearer your-secret-token" http://localhost:8080/api/status
```

If the token is missing or incorrect, the server returns `401 Unauthorized` with a `WWW-Authenticate: Bearer realm="stackd"` header.

### Always-Public Endpoints

The following endpoints bypass auth regardless of `DASHBOARD_TOKEN`:

| Endpoint | Reason |
|---|---|
| `GET /` | Dashboard HTML (no secrets exposed) |
| `GET /assets/*` | Static JS/CSS bundles |
| `GET /healthz` | Liveness probe — must be accessible to orchestrators |
| `GET /readyz` | Readiness probe — must be accessible to orchestrators |
| `GET /metrics` | Prometheus scrape — typically protected at the network level |

---

## Rate Limiting

Manual sync requests (`POST /api/sync/{repo}`) are rate-limited per repo to prevent abuse. The default window is **5 seconds**; configure it with `SYNC_RATE_LIMIT_SECONDS`:

```yaml
environment:
  - SYNC_RATE_LIMIT_SECONDS=10   # at most one manual sync per 10 seconds per repo
```

When the limit is exceeded the server returns `429 Too Many Requests`:

```json
{"status": "rate_limited", "repo": "myrepo"}
```

---

## Security Response Headers

stackd adds the following headers to every HTTP response:

| Header | Value | Purpose |
|---|---|---|
| `X-Content-Type-Options` | `nosniff` | Prevents MIME-sniffing attacks |
| `X-Frame-Options` | `DENY` | Blocks clickjacking via iframes |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limits referrer information leakage |

---

## Secrets Masking in Logs

Environment variable values whose names contain `TOKEN`, `SECRET`, `KEY`, `PASSWORD`, `PASS`, or `CREDENTIAL` are automatically replaced with `[redacted]` in all structured log output. Infisical tokens, dashboard tokens, and SSH key paths are never logged in plaintext.

---

## Production Hardening

### Always Set DASHBOARD_TOKEN

Without a token, anyone who can reach the dashboard port can trigger syncs and read your stack state. Generate a strong token:

```sh
openssl rand -hex 32
```

### Use PULL_ONLY=true

Unless your workflow requires stackd to push commits, enable pull-only mode to prevent any git write operations:

```yaml
environment:
  - PULL_ONLY=true
```

### Run Behind a Reverse Proxy with TLS

Expose stackd through nginx or Caddy rather than directly on the internet. Example Caddy snippet:

```
stackd.example.com {
    reverse_proxy localhost:8080
}
```

Example nginx snippet:

```nginx
server {
    listen 443 ssl;
    server_name stackd.example.com;

    ssl_certificate     /etc/ssl/certs/stackd.crt;
    ssl_certificate_key /etc/ssl/private/stackd.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # Required for SSE log streaming
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

### Restrict Docker Socket Access

The Docker socket (`/var/run/docker.sock`) grants root-equivalent access to the host. Consider using a Docker socket proxy (e.g. [Tecnativa/docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy)) to restrict which Docker API calls stackd can make.
