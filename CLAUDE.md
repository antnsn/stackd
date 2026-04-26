# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->


## Pre-Push & Pre-Completion Code Review

**MANDATORY for every agent (Claude, Copilot CLI, Codex, Cursor, etc.):**

- Before declaring a task complete **and** before any `git push`, run a code review using your client's review skill. Examples:
  - Copilot CLI: `/copilot:review`
  - Codex CLI: `/codex:review`
  - Any equivalent automated reviewer the client provides.
- Address every finding from the review, or explicitly justify in writing why a finding is not actionable, before pushing or marking the task done.
- If the review is skipped or fails, the push / task-complete state is **not** allowed. Fix the issues locally and re-run the review until clean.
- This applies to bugfixes, doc-only changes, dependency bumps, and "trivial" edits — no exceptions.

## Build & Test

```bash
# 1. Build frontend first — Go embeds internal/ui/dist via //go:embed
cd internal/ui && npm install && npm run build && cd ../..

# 2. Go binary (run from repo root)
go build ./...
go vet  ./...
go test ./...

# 3. Container image (multi-stage: node → go → alpine; runs from repo root)
docker build -t stackd:dev -f dockerfile .
```

`internal/ui/dist/` is gitignored and embedded via `embed.FS` from `internal/ui/ui.go`. The UI **must** be built before any Go command that loads the embed package on a fresh checkout, otherwise `go build` fails with `pattern dist/...: no matching files found`. The Dockerfile handles this automatically (node stage runs before the go stage).

## Architecture Overview

stackd is a single-binary GitOps daemon for Docker Compose stacks.

```
main()
  └─ setupSSH()                          # Write ssh config to /tmp/stackd-ssh
  └─ New Store / New Docker Client
  └─ runStacksSync()                     # Startup: discover & apply all stacks
  └─ Start HTTP server (goroutine)       # Dashboard + REST API + SSE logs
  └─ Main loop (ticker + syncTrigger):
       └─ syncRepo()                     # fetch → compare SHA → pull → runStacksSync
            └─ applyStack()              # docker compose up -d (or infisical run …)
       └─ refreshContainers()            # Update container state in Store
```

Layout (see `.github/copilot-instructions.md` for the full tree and tech stack):

| Path | Role |
|---|---|
| `main.go` | Application entrypoint, sync loop, Infisical wrapper, startup |
| `internal/state/` | Thread-safe in-memory state store |
| `internal/git/` | Git operations (`execpg`-wrapped, hardened SSH) |
| `internal/execpg/` | `exec.Cmd` helper that kills the whole process group on context cancel — fixes subprocess PID leaks |
| `internal/docker/` | Docker API wrapper (container inspect, log stream) |
| `internal/server/` | HTTP server (dashboard + REST + SSE) |
| `internal/metrics/` | Prometheus exposition (`stackd_goroutines`, `stackd_threads`, …) |
| `internal/ui/` | Preact frontend (built → embedded in binary) |
| `dockerfile` | 3-stage: node → go → alpine |
| `.github/copilot-instructions.md` | Canonical project context, design system, delivery agents |
| `.github/agents/` | Specialized delivery agents (per-area scope) |
| `docs/` | User-facing documentation |

## Conventions & Patterns

The full source of truth is `.github/copilot-instructions.md`. Highlights:

- **Go:** Standard library preferred. New packages go under `internal/`. Wrap errors with context (`fmt.Errorf("syncRepo %s: %w", name, err)`).
- **Logging:** New code uses `log/slog` (structured, JSON). Do not use `log.Printf`.
- **Context:** All long-running operations accept and respect a `context.Context` with a deadline. Never use `context.Background()` without a timeout in a goroutine.
- **Subprocesses:** Use `internal/execpg.CommandContext` instead of `exec.CommandContext` for any command that may spawn grandchildren (ssh, docker compose, infisical, …). This sets `Setpgid=true` and SIGKILLs the whole group on cancel — required to avoid PID leaks (root cause of past `runtime: newosproc EAGAIN` crashes).
- **Concurrency:** All shared state goes through `internal/state.Store`. No direct mutation of shared variables outside the store.
- **Testing:** Table-driven `_test.go` files alongside new code. Use `t.Cleanup` for teardown.
- **Frontend:** Small focused Preact components, CSS modules per component, no external UI libraries. Dark only (`#0d1117`). `JetBrains Mono` for data/logs; `DM Sans` for labels. Status colors are reserved (`#3fb950`/`#f85149`/`#d29922`/`#58a6ff`).
- **Comments:** Only when something is non-obvious. Self-documenting names first.
- **Module name:** `stackd` (`go.mod` line 1).
