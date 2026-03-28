---
description: "Workspace-level instructions for GitHub Copilot on the stackd project."
---

# stackd — Copilot Workspace Instructions

## What is stackd?

**stackd** is a GitOps daemon for Docker Compose stacks. It watches Git repositories
for changes and automatically applies `docker compose up -d`, with optional Infisical
secrets injection and a built-in web dashboard for monitoring stack status, container
health, and real-time logs. Target audience: home lab operators and small DevOps teams.

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25, single binary |
| Module | `simpleGithubSync` (go.mod) |
| Frontend | Preact + Vite (embedded in binary via `embed.FS`) |
| Secrets | Infisical CLI (optional, per-stack or global) |
| Container runtime | Docker API (`github.com/docker/docker`) |
| CI/CD | GitHub Actions → GHCR image (`ghcr.io/<owner>/stackd`) |
| Runtime image | Alpine Linux (multi-stage Dockerfile) |

## Repository Layout

```
stackd/
├── main.go                    # All application logic (sync, git, Infisical, startup)
├── internal/
│   ├── state/state.go         # Thread-safe in-memory state store
│   ├── docker/client.go       # Docker API wrapper (container inspect, log stream)
│   ├── server/server.go       # HTTP server (dashboard + REST API + SSE logs)
│   └── ui/                    # Preact frontend (built → embedded in binary)
│       └── src/components/
│           ├── AppGrid.jsx    # Container grid
│           └── AppDetail.jsx  # Log streaming detail panel
├── dockerfile                 # 3-stage: node → go → alpine
└── .github/
    ├── copilot-instructions.md  ← you are here
    ├── agents/                  # Specialized delivery agents
    └── workflows/docker-publish.yml
```

## Coding Conventions

- **Go:** Standard library preferred; minimal new dependencies. All new packages go under `internal/`.
- **Error handling:** Always wrap errors with context (`fmt.Errorf("syncRepo %s: %w", name, err)`). Never swallow errors silently.
- **Logging:** New code must use `log/slog` (structured, JSON-outputting). Do NOT use `log.Printf`.
- **Context:** All long-running operations must accept and respect a `context.Context` with a timeout. Never use `context.Background()` without a deadline in goroutines.
- **Concurrency:** All shared state goes through `internal/state.Store`. No direct mutation of shared variables outside the store.
- **Testing:** All new Go code must have corresponding `_test.go` files. Use table-driven tests. Use `t.Cleanup` to tear down resources.
- **Frontend:** Keep Preact components small and focused. CSS modules per component. No external UI libraries.
- **Comments:** Only comment non-obvious logic. Self-documenting names are preferred.

## Key Data Flow

```
main()
  └─ setupSSH()                         # Write ssh config to /tmp/stackd-ssh
  └─ New Store / New Docker Client
  └─ runStacksSync()                    # Startup: discover & apply all stacks
  └─ Start HTTP server (goroutine)
  └─ Main loop (ticker + syncTrigger channel):
       └─ syncRepo(name, cfg)           # fetch → compare SHA → pull → runStacksSync
            └─ applyStack(cfg, stackDir) # docker compose up -d (or infisical run …)
       └─ refreshContainers()           # Update container state in Store
```

## Environment Variables (Configuration)

| Variable | Default | Description |
|---|---|---|
| `REPOS_DIR` | `/repos` | Parent directory of mounted Git repos |
| `STACKS_DIR_<REPO>` | (required) | Path to compose stacks for each repo |
| `SYNC_INTERVAL_SECONDS` | `60` | Polling interval |
| `PULL_ONLY` | `false` | Disable git push back to origin |
| `GIT_USER_NAME` | `githubSync` | Git commit author name |
| `GIT_USER_EMAIL` | `githubsync@localhost` | Git commit author email |
| `DASHBOARD_ENABLED` | `true` | Enable web dashboard |
| `DASHBOARD_PORT` | `8080` | HTTP listen port |
| `INFISICAL_TOKEN` | `` | Global Infisical machine token |
| `INFISICAL_ENV` | `prod` | Global Infisical environment |
| `INFISICAL_URL` | `` | Self-hosted Infisical instance URL |
| `DEV_SEED` | `false` | Seed dashboard with mock data (dev only) |

Per-repo branch and remote configuration will be added — see `agents/config-refactor.agent.md`.

## Known Issues & Technical Debt

1. **Branch hardcoded to `main`** — `git pull origin main` / `git push origin main` in `main.go:363,429`. CI workflow targets `master`. Mismatch will cause sync failures on non-`main` repos.
2. **`goto` statements** — `main.go:173` and `main.go:553`. Refactor to structured control flow.
3. **No tests** — Zero coverage. All new code must be tested.
4. **No authentication on dashboard** — Anyone with network access can trigger syncs and read logs.
5. **No graceful shutdown** — No SIGTERM handler; kill -9 only way to stop; can corrupt in-flight git operations.
6. **Unused OpenTelemetry** — `go.opentelemetry.io/*` imported transitively but never used. Bloats image.
7. **No structured logging** — Using `log.Printf`. Must migrate to `log/slog`.
8. **No health endpoints** — No `/healthz` or `/readyz`. Kubernetes / container orchestrators cannot probe liveness.
9. **No retry / backoff** — Failed syncs immediately retry on next tick. Cascading failure risk.
10. **Context timeouts missing** — Long-running git and docker operations can hang indefinitely.

## Delivery Agents

See `.github/agents/` for specialized agents covering each delivery area:

| Agent file | Responsibility |
|---|---|
| `delivery-lead.agent.md` | Orchestrates full delivery; knows all agents and their order |
| `code-quality.agent.md` | Fix `goto`, hardcoded branch/remote, OTel cleanup, context timeouts |
| `resilience.agent.md` | SIGTERM graceful shutdown, retry+backoff, Docker client reconnection |
| `security.agent.md` | Dashboard token auth, API rate limiting, secrets masking in logs |
| `observability.agent.md` | `slog` structured logging, Prometheus `/metrics`, `/healthz` + `/readyz` |
| `testing.agent.md` | Unit tests for state, docker, server; integration test for sync loop |
| `config-refactor.agent.md` | Configurable branch/remote per repo; optional YAML config file |
| `ui-improvements.agent.md` | Fix stacks-vs-containers UX, add commit history, multi-log select |
