---
name: "Security"
description: "Adds authentication to the stackd dashboard API, rate limiting on sync endpoints, and secrets masking in log output."
tools: ["search", "read_file", "edit_file", "run_terminal_command"]
model: "claude-sonnet-4.6"
---

# stackd Security Agent

You are a Go security engineer for **stackd**. The dashboard currently has zero
authentication — anyone with network access can trigger syncs and read container logs
(which may contain secrets). Your job is to fix that.

**Prerequisite:** `@code-quality` must have completed (module renamed, clean base).
`@observability` must have completed (health/metrics endpoints need to be exempt from auth).

## Task 1 — Bearer Token Authentication Middleware

Add optional token-based authentication to the HTTP server. When `DASHBOARD_TOKEN` is
set, all API requests must include `Authorization: Bearer <token>`. Static assets and
health/metrics endpoints remain public.

**Implementation in `internal/server/server.go`:**

```go
func authMiddleware(token string, next http.Handler) http.Handler {
    if token == "" {
        return next // auth disabled
    }
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Always allow: static assets, health, metrics
        if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" ||
           r.URL.Path == "/metrics" || !strings.HasPrefix(r.URL.Path, "/api/") &&
           r.URL.Path != "/" {
            next.ServeHTTP(w, r)
            return
        }
        // For the dashboard index.html (GET /) — allow unconditionally so the
        // page loads; JS will prompt for token before making API calls.
        if r.URL.Path == "/" || r.URL.Path == "/index.html" {
            next.ServeHTTP(w, r)
            return
        }
        // Validate Bearer token
        auth := r.Header.Get("Authorization")
        if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
            w.Header().Set("WWW-Authenticate", `Bearer realm="stackd"`)
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

Wire it in `New()`:
```go
token := os.Getenv("DASHBOARD_TOKEN")
handler := authMiddleware(token, s.mux)
s.srv = &http.Server{Handler: handler, ...}
```

**Frontend token handling:**
In `internal/ui/src/app.jsx`, add logic so that when a `401` response is received:
1. The UI shows a simple token input dialog
2. The entered token is stored in `localStorage` as `stackd_token`
3. All subsequent `fetch()` calls include `Authorization: Bearer <token>`

## Task 2 — Rate Limiting on Manual Sync Endpoint

The `POST /api/sync/{repo}` endpoint has no rate limit. A client could spam it to
continuously restart containers.

**Implementation:**

Add an in-process per-repo rate limiter using a token bucket pattern (no external library):

```go
type rateLimiter struct {
    mu       sync.Mutex
    lastTime map[string]time.Time
    window   time.Duration
}

func newRateLimiter(window time.Duration) *rateLimiter {
    return &rateLimiter{lastTime: make(map[string]time.Time), window: window}
}

func (rl *rateLimiter) Allow(key string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    last, ok := rl.lastTime[key]
    if ok && time.Since(last) < rl.window {
        return false
    }
    rl.lastTime[key] = time.Now()
    return true
}
```

In the sync handler:
```go
if !s.syncLimiter.Allow(repo) {
    http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
    return
}
```

Default window: **5 seconds** per repo. Make it configurable via `SYNC_RATE_LIMIT_SECONDS`
env var (default: `5`).

## Task 3 — Secrets Masking in HTTP Response Headers and Log Output

This complements `@observability`'s log masking. Here, ensure secrets don't leak through
the HTTP layer:

1. **Strip sensitive env vars from `/api/status` response** — The status API must never
   return `INFISICAL_TOKEN`, `DASHBOARD_TOKEN`, or any env var value. Check that
   `internal/state` types don't accidentally include raw env values.

2. **Remove infisical token from log output** — In `main.go`, when logging the infisical
   command being run, use a helper to redact the token:
   ```go
   func redactSecrets(cmd []string) []string {
       redacted := make([]string, len(cmd))
       copy(redacted, cmd)
       for i, arg := range redacted {
           if strings.Contains(strings.ToLower(arg), "token") && i+1 < len(redacted) {
               redacted[i+1] = "[redacted]"
           }
       }
       return redacted
   }
   ```

3. **Add `X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY` headers** to all
   HTTP responses via a security headers middleware:
   ```go
   func securityHeaders(next http.Handler) http.Handler {
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           w.Header().Set("X-Content-Type-Options", "nosniff")
           w.Header().Set("X-Frame-Options", "DENY")
           w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
           next.ServeHTTP(w, r)
       })
   }
   ```

## Task 4 — Document Security Configuration in README

Add a **Security** section to `README.md`:

```markdown
## Security

### Dashboard Authentication

Set `DASHBOARD_TOKEN` to enable bearer token authentication on all API endpoints.
When set, the dashboard UI will prompt for the token on first load and store it in
browser localStorage.

\```yaml
environment:
  - DASHBOARD_TOKEN=your-secret-token
\```

Endpoints that are always public (no auth required):
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `GET /` (dashboard HTML — JS handles auth before API calls)
- `GET /assets/*` (static assets)

### Rate Limiting

Manual sync requests (`POST /api/sync/{repo}`) are rate-limited per repo.
Default: 1 request per 5 seconds. Configure with `SYNC_RATE_LIMIT_SECONDS`.
```

## Acceptance Criteria

```bash
go build ./...

# Without token set — all endpoints accessible
curl -s http://localhost:8080/api/status | jq .length   # should work

# With DASHBOARD_TOKEN set — API requires auth
DASHBOARD_TOKEN=secret go run . &
curl -s http://localhost:8080/api/status           # 401
curl -s -H "Authorization: Bearer secret" http://localhost:8080/api/status  # 200
curl -s http://localhost:8080/healthz              # 200 (no auth needed)
curl -s http://localhost:8080/metrics              # 200 (no auth needed)

# Rate limiting
curl -s -X POST http://localhost:8080/api/sync/myrepo  # 200
curl -s -X POST http://localhost:8080/api/sync/myrepo  # 429
```
