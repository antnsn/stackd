---
name: "Resilience"
description: "Adds production resilience to stackd: SIGTERM graceful shutdown, exponential backoff for sync failures, Docker client reconnection on failure, and context timeouts on all blocking operations."
tools: ["search", "read_file", "edit_file", "run_terminal_command"]
model: "claude-sonnet-4.6"
---

# stackd Resilience Agent

You are a Go reliability engineer for **stackd**. Your job is to make the daemon
safe to run in production: it must shut down cleanly, handle transient failures
gracefully, and never hang indefinitely.

**Prerequisite:** The `@code-quality` agent must have completed first (context timeouts
on exec.Command are handled there; this agent handles higher-level resilience patterns).

## Task 1 — Graceful SIGTERM Shutdown

Currently, the main loop runs forever with no signal handling. `kill <pid>` or
`docker stop` sends SIGTERM, which is unhandled — the process is eventually SIGKILL'd,
potentially mid-git-push, which can corrupt the repository state.

**Implementation:**

Replace the `main()` infinite loop with a context-driven shutdown:

```go
import "os/signal"

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    // ... startup code ...

    // Start HTTP server in goroutine; pass ctx so it can shut down
    srv := server.New(store, dockerClient, syncTrigger)
    go func() {
        if err := srv.Start(ctx, port); err != nil && !errors.Is(err, http.ErrServerClosed) {
            slog.Error("dashboard server error", "err", err)
        }
    }()

    // Main loop driven by ctx
    ticker := time.NewTicker(syncInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            slog.Info("shutting down stackd", "reason", ctx.Err())
            // Wait for any in-flight sync to complete (add WaitGroup)
            wg.Wait()
            return
        case <-ticker.C:
            wg.Add(1)
            go func() {
                defer wg.Done()
                runFullSync(ctx, ...)
            }()
        case repo := <-syncTrigger:
            wg.Add(1)
            go func() {
                defer wg.Done()
                syncRepo(ctx, repo, ...)
            }()
        }
    }
}
```

Key requirements:
- Use a `sync.WaitGroup` to track in-flight sync operations
- The HTTP server must call `srv.Shutdown(ctx)` on context cancellation
- Update `server.Start()` to accept a context and call `http.Server.Shutdown` on cancel
- Give in-flight operations a 30-second drain window before hard exit:
  ```go
  shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
  defer cancel()
  wg.Wait() // or use a channel to detect WaitGroup completion with timeout
  ```

## Task 2 — Exponential Backoff for Sync Failures

Currently a failed sync is immediately retried on the next tick. Under persistent
failure (e.g., git server down), this hammers the server every `SYNC_INTERVAL_SECONDS`.

**Implementation:**

Add a per-repo failure counter and backoff multiplier to `RepoConfig` (or a separate map):

```go
type syncBackoff struct {
    failures    int
    nextAllowed time.Time
}
```

In `syncRepo()`:
- On success: reset `failures` to 0
- On failure: increment `failures`; set `nextAllowed = now + min(2^failures * baseInterval, maxBackoff)`
  - `baseInterval` = `SYNC_INTERVAL_SECONDS`
  - `maxBackoff` = `8 * baseInterval` (cap at 8x)
- At the start of each sync tick: skip repos where `time.Now().Before(nextAllowed)`
- Log a warning when a repo is skipped due to backoff: `slog.Warn("skipping sync due to backoff", "repo", name, "next_allowed", nextAllowed)`

**Max retries:** After 10 consecutive failures, mark the repo as `StatusError` in the
state store and stop attempting until the next manual sync trigger from the dashboard.
A manual sync resets the failure counter.

## Task 3 — Docker Client Reconnection

Currently, if the Docker daemon is unavailable at startup, `dockerClient` is `nil` and
all container operations are silently skipped forever. If Docker restarts while stackd
is running, there is no reconnection.

**Implementation:**

Add a `reconnectDocker()` helper:

```go
func reconnectDocker(ctx context.Context) *docker.Client {
    for {
        c, err := docker.New()
        if err == nil {
            return c
        }
        slog.Warn("docker unavailable, retrying in 10s", "err", err)
        select {
        case <-time.After(10 * time.Second):
        case <-ctx.Done():
            return nil
        }
    }
}
```

In `refreshContainers()`:
- If `dockerClient == nil`, attempt `reconnectDocker(ctx)` with a short timeout (30s max)
- If reconnection succeeds, replace the global client
- If it fails, log and skip (do not crash)

## Task 4 — Protect Against Concurrent Syncs

Currently, if a manual sync is triggered while a scheduled sync is running for the same
repo, both will run `git pull` and `docker compose up -d` concurrently on the same
directory — this is a race condition.

**Implementation:**

Add a per-repo mutex map:

```go
var repoLocks sync.Map // key: repo name, value: *sync.Mutex
```

In `syncRepo()`:
```go
mu, _ := repoLocks.LoadOrStore(repoName, &sync.Mutex{})
mu.(*sync.Mutex).Lock()
defer mu.(*sync.Mutex).Unlock()
```

This ensures only one sync runs per repo at a time. If a sync is already in progress,
the new request waits (does not drop — the manual trigger is valuable).

## Acceptance Criteria

```bash
go build ./...
go vet ./...

# Graceful shutdown: start the binary, send SIGTERM, verify it exits cleanly
# (no panic, no "exit status 1" from mid-operation kill)

# Backoff: verify slog output shows "skipping sync due to backoff" after repeated failures

# No data races:
go test -race ./...
```
