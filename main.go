package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cryptoModule "stackd/internal/crypto"
	"stackd/internal/db"
	"stackd/internal/docker"
	"stackd/internal/git"
	"stackd/internal/metrics"
	"stackd/internal/server"
	"stackd/internal/state"
)

// syncBackoff tracks retry state for a single repo.
type syncBackoff struct {
mu          sync.Mutex
failures    int
nextAllowed time.Time
suspended   bool // true after maxFailures consecutive failures
}

const maxSyncFailures = 10

var repoBackoffs sync.Map // key: repo name (string), value: *syncBackoff
var repoLocks sync.Map   // key: repo name (string), value: *sync.Mutex

func getBackoff(repoName string) *syncBackoff {
v, _ := repoBackoffs.LoadOrStore(repoName, &syncBackoff{})
return v.(*syncBackoff)
}

// recordSyncSuccess resets the backoff for a repo.
func recordSyncSuccess(repoName string) {
b := getBackoff(repoName)
b.mu.Lock()
defer b.mu.Unlock()
b.failures = 0
b.nextAllowed = time.Time{}
b.suspended = false
}

// recordSyncFailure increments failure count and sets next allowed time.
// Returns true if the repo is now suspended (max failures reached).
func recordSyncFailure(repoName string, baseInterval time.Duration) bool {
b := getBackoff(repoName)
b.mu.Lock()
defer b.mu.Unlock()
b.failures++
if b.failures >= maxSyncFailures {
b.suspended = true
slog.Warn("repo suspended after consecutive failures", "repo", repoName, "failures", b.failures)
return true
}
multiplier := time.Duration(1 << b.failures) // 2, 4, 8, 16...
backoff := multiplier * baseInterval
maxBackoff := 8 * baseInterval
if backoff > maxBackoff {
backoff = maxBackoff
}
b.nextAllowed = time.Now().Add(backoff)
slog.Warn("sync backoff", "repo", repoName, "backoff", backoff, "failure", b.failures, "maxFailures", maxSyncFailures)
return false
}

// shouldSkipSync returns true if the repo is in backoff or suspended.
func shouldSkipSync(repoName string) bool {
b := getBackoff(repoName)
b.mu.Lock()
defer b.mu.Unlock()
if b.suspended {
return true
}
if !b.nextAllowed.IsZero() && time.Now().Before(b.nextAllowed) {
return true
}
return false
}

// resetBackoff resets backoff for a repo (called on manual sync trigger).
func resetBackoff(repoName string) {
b := getBackoff(repoName)
b.mu.Lock()
defer b.mu.Unlock()
b.failures = 0
b.nextAllowed = time.Time{}
b.suspended = false
}

// redactSecretEnv returns "[redacted]" if the env var name looks sensitive.
func redactSecretEnv(key, value string) string {
	upper := strings.ToUpper(key)
	for _, s := range []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "PASS", "CREDENTIAL"} {
		if strings.Contains(upper, s) {
			return "[redacted]"
		}
	}
	return value
}

// applyStack runs "docker compose up -d" for a single stack directory.
//
// Infisical secrets injection is applied when INFISICAL_ENABLED=true.
// Auth priority:
//  1. Per-stack infisical.toml in the stack directory (--config=<path>)
//  2. Global INFISICAL_TOKEN + INFISICAL_ENV env vars
//
// If neither token nor toml is available and INFISICAL_ENABLED=true, a warning
// is logged and the stack is applied without secrets injection.
// INFISICAL_URL can point to a self-hosted Infisical instance.

// buildComposeCmd constructs the exec.Cmd to apply a stack. It returns either a bare
// "docker compose up -d" or an "infisical run -- docker compose up -d" command depending
// on the INFISICAL_ENABLED env var and available credentials. The provided ctx is used
// directly, so callers should set an appropriate deadline before calling.
func buildComposeCmd(ctx context.Context, stackPath, stackName string) *exec.Cmd {
if strings.ToLower(os.Getenv("INFISICAL_ENABLED")) != "true" {
slog.Info("applying stack", "stack", stackName, "infisical", false)
return exec.CommandContext(ctx, "docker", "compose", "up", "-d")
}

args := []string{"run"}
configured := false

tomlPath := filepath.Join(stackPath, "infisical.toml")
if _, err := os.Stat(tomlPath); err == nil {
args = append(args, "--config="+tomlPath)
slog.Info("stack using per-stack infisical.toml", "stack", stackName)
configured = true
} else if token := os.Getenv("INFISICAL_TOKEN"); token != "" {
args = append(args, "--token="+token)
infisicalEnv := os.Getenv("INFISICAL_ENV")
if infisicalEnv == "" {
infisicalEnv = "prod"
}
args = append(args, "--env="+infisicalEnv)
slog.Info("stack using global infisical token", "stack", stackName, "env", infisicalEnv)
configured = true
} else {
slog.Warn("INFISICAL_ENABLED=true but no credentials found, applying without secrets injection", "stack", stackName)
}

if configured {
if infisicalURL := os.Getenv("INFISICAL_URL"); infisicalURL != "" {
args = append(args, "--domain="+infisicalURL)
}
args = append(args, "--", "docker", "compose", "up", "-d")
return exec.CommandContext(ctx, "infisical", args...)
}

return exec.CommandContext(ctx, "docker", "compose", "up", "-d")
}

// refreshContainers updates container details for all stacks whose StackDir is
// known. Safe to call with a nil dockerClientPtr (no-op). Attempts reconnection
// if *dockerClientPtr is nil.
func refreshContainers(ctx context.Context, store *state.Store, dockerClientPtr **docker.Client) {
if dockerClientPtr == nil {
return
}
if *dockerClientPtr == nil {
reconnCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
defer cancel()
if c, err := docker.New(); err == nil {
slog.Info("docker client reconnected")
*dockerClientPtr = c
} else {
slog.Warn("docker reconnection failed", "err", err)
_ = reconnCtx
return
}
}
for _, st := range store.GetAllStacks() {
if st.StackDir == "" {
continue
}
refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
ctrs, err := (*dockerClientPtr).ListStackContainerDetails(refreshCtx, st.StackDir)
cancel()
if err != nil {
slog.Warn("refreshContainers failed", "repo", st.RepoName, "stack", st.Name, "err", err)
ctrs = nil
}
containers := make([]state.ContainerDetail, 0, len(ctrs))
for _, dc := range ctrs {
containers = append(containers, state.ContainerDetail{
ID:        dc.ID,
Name:      dc.Name,
Image:     dc.Image,
Status:    dc.Status,
StartedAt: dc.StartedAt,
			Env:       dc.Env,
			Ports:     dc.Ports,
		})
}
store.UpdateStackContainers(st.RepoName, st.Name, containers)
		running := int64(0)
		for _, c := range containers {
			if c.Status == "running" {
				running++
			}
		}
		metrics.SetContainersRunning(st.RepoName+"/"+st.Name, running)
	}
}

// latestStartedAt returns the most recent StartedAt time across containers,
// or the zero time if none are present. Used so stack.LastApply reflects
// when containers actually last changed rather than when stackd last ran.
func latestStartedAt(containers []state.ContainerDetail) time.Time {
	var latest time.Time
	for _, c := range containers {
		if c.StartedAt.After(latest) {
			latest = c.StartedAt
		}
	}
	return latest
}


func applyStack(ctx context.Context, stackPath, stackName, repoName string, store *state.Store, dockerClient *docker.Client) {
if store != nil {
store.UpdateStack(state.StackState{
Name:       stackName,
RepoName:   repoName,
StackDir:   stackPath,
Status:     state.ApplyApplying,
Containers: []state.ContainerDetail{},
})
}

applyCtx, applyCancel := context.WithTimeout(ctx, 300*time.Second)
defer applyCancel()
cmd := buildComposeCmd(applyCtx, stackPath, stackName)
cmd.Dir = stackPath
output, err := cmd.CombinedOutput()
outputStr := string(output)
if len(output) > 0 {
slog.Info("stack compose output", "stack", stackName, "output", outputStr)
}

if store != nil {
st := state.StackState{
Name:       stackName,
RepoName:   repoName,
StackDir:   stackPath,
LastApply:  time.Now(),
LastOutput: outputStr,
Containers: []state.ContainerDetail{},
}
if err != nil {
st.Status = state.ApplyError
st.LastError = err.Error()
} else {
st.Status = state.ApplyOK
if dockerClient != nil {
ctrCtx, ctrCancel := context.WithTimeout(ctx, 30*time.Second)
ctrs, dErr := dockerClient.ListStackContainerDetails(ctrCtx, stackPath)
ctrCancel()
if dErr != nil {
slog.Warn("container lookup failed", "stack", stackName, "err", dErr)
} else {
for _, dc := range ctrs {
st.Containers = append(st.Containers, state.ContainerDetail{
ID:        dc.ID,
Name:      dc.Name,
Image:     dc.Image,
Status:    dc.Status,
StartedAt: dc.StartedAt,
				Env:       dc.Env,
				Ports:     dc.Ports,
			})
}
				// Use the most recent container StartedAt so the stack card
				// shows when containers actually last changed, not when
				// stackd last ran docker compose (resets on every restart).
				if latest := latestStartedAt(st.Containers); !latest.IsZero() {
					st.LastApply = latest
				}
}
}
}
store.UpdateStack(st)
	if err != nil {
		metrics.RecordApply(stackName, "error")
	} else {
		metrics.RecordApply(stackName, "success")
	}
}

if err != nil {
slog.Error("stack apply failed", "stack", stackName, "err", err)
} else {
slog.Info("stack applied successfully", "stack", stackName)
}
}


// runStacksSync discovers and applies all docker-compose stacks in stacksDir.
func runStacksSync(ctx context.Context, stacksDir, repoName string, store *state.Store, dockerClient *docker.Client) {
	if stacksDir == "" {
		return
	}
	entries, err := os.ReadDir(stacksDir)
	if err != nil {
		slog.Error("failed to read stacks dir", "repo", repoName, "dir", stacksDir, "err", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stackName := entry.Name()
		stackPath := filepath.Join(stacksDir, stackName)
		composePath := ""
		for _, candidate := range []string{"compose.yaml", "docker-compose.yml"} {
			p := filepath.Join(stackPath, candidate)
			if _, err := os.Stat(p); err == nil {
				composePath = p
				break
			}
		}
		if composePath == "" {
			slog.Warn("no compose file found, skipping stack", "stack", stackName, "repo", repoName)
			continue
		}
		applyStack(ctx, stackPath, stackName, repoName, store, dockerClient)
	}
}

// syncRepoFromDB clones or pulls the repo and applies stacks if the SHA changed.
func syncRepoFromDB(ctx context.Context, repo db.RepoDB, cloneDir string, cryptoKey []byte, sqlDB *sql.DB, syncInterval time.Duration, store *state.Store, dockerClient *docker.Client) {
	repoName := repo.Name
	start := time.Now()

	if store != nil {
		existing, ok := store.GetRepo(repoName)
		if !ok {
			existing = state.RepoState{Name: repoName}
		}
		existing.Status = state.StatusSyncing
		store.UpdateRepo(existing)
	}

	recordError := func(msg string) {
		slog.Error("sync error", "repo", repoName, "detail", msg)
		if store != nil {
			existing, _ := store.GetRepo(repoName)
			existing.Name = repoName
			existing.Status = state.StatusError
			existing.LastError = msg
			store.UpdateRepo(existing)
		}
		recordSyncFailure(repoName, syncInterval)
		metrics.RecordSync(repoName, "error", time.Since(start))
	}

	lockVal, _ := repoLocks.LoadOrStore(repoName, &sync.Mutex{})
	repoMu := lockVal.(*sync.Mutex)
	repoMu.Lock()
	defer repoMu.Unlock()

	opts := git.AuthOpts{Type: repo.AuthType}
	switch repo.AuthType {
	case "ssh":
		if repo.SSHKeyID != "" {
			keyCtx, keyCancel := context.WithTimeout(ctx, 10*time.Second)
			sshKey, err := db.GetSSHKey(keyCtx, sqlDB, repo.SSHKeyID)
			keyCancel()
			if err != nil {
				recordError(fmt.Sprintf("get SSH key: %v", err))
				return
			}
			privKey, err := cryptoModule.Decrypt(cryptoKey, sshKey.PrivateKeyEnc)
			if err != nil {
				recordError(fmt.Sprintf("decrypt SSH key: %v", err))
				return
			}
			keyPath := fmt.Sprintf("/tmp/stackd-key-%s", repo.ID)
			if err := os.WriteFile(keyPath, []byte(privKey), 0600); err != nil {
				recordError(fmt.Sprintf("write SSH key file: %v", err))
				return
			}
			opts.SSHKeyPath = keyPath
		}
	case "pat":
		if repo.PATEnc != "" {
			pat, err := cryptoModule.Decrypt(cryptoKey, repo.PATEnc)
			if err != nil {
				recordError(fmt.Sprintf("decrypt PAT: %v", err))
				return
			}
			opts.PAT = pat
		}
	}

	destDir := filepath.Join(cloneDir, repo.Name)

	shaCtx, shaCancel := context.WithTimeout(ctx, 10*time.Second)
	oldSHA, _ := git.HeadSHA(shaCtx, destDir)
	shaCancel()

	cloneCtx, cloneCancel := context.WithTimeout(ctx, 120*time.Second)
	err := git.Clone(cloneCtx, repo.URL, destDir, opts)
	cloneCancel()
	if err != nil {
		recordError(fmt.Sprintf("git clone/pull failed: %v", err))
		return
	}

	newSHACtx, newSHACancel := context.WithTimeout(ctx, 10*time.Second)
	newSHA, _ := git.HeadSHA(newSHACtx, destDir)
	newSHACancel()

	if newSHA != oldSHA || oldSHA == "" {
		stacksDir := filepath.Join(destDir, repo.StacksDir)
		runStacksSync(ctx, stacksDir, repoName, store, dockerClient)
	}

	if store != nil {
		store.UpdateRepo(state.RepoState{
			Name:     repoName,
			LastSync: time.Now(),
			LastSHA:  newSHA,
			Status:   state.StatusOK,
		})
	}
	recordSyncSuccess(repoName)
	metrics.RecordSync(repoName, "success", time.Since(start))
}

func main() {
	// Configure structured logger
	logLevel := new(slog.LevelVar)
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		logLevel.Set(slog.LevelDebug)
	case "warn":
		logLevel.Set(slog.LevelWarn)
	case "error":
		logLevel.Set(slog.LevelError)
	default:
		logLevel.Set(slog.LevelInfo)
	}
	var handler slog.Handler
	if strings.ToLower(os.Getenv("LOG_FORMAT")) == "text" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	}
	slog.SetDefault(slog.New(handler))

	// --- Bootstrap env vars ---
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		dbURL = "sqlite://stackd.db"
	}
	port := 8080
	if portStr := os.Getenv("PORT"); portStr != "" {
		if v, err := strconv.Atoi(portStr); err == nil && v > 0 {
			port = v
		} else {
			slog.Warn("invalid PORT value, using default 8080", "value", portStr)
		}
	}
	secretKey := os.Getenv("SECRET_KEY")
	if secretKey == "" {
		slog.Error("SECRET_KEY environment variable is required for encrypting sensitive configuration")
		os.Exit(1)
	}
	cloneDir := os.Getenv("CLONE_DIR")
	if cloneDir == "" {
		cloneDir = "/var/lib/stackd/repos"
	}

	syncIntervalSeconds := 60
	if v, err := strconv.Atoi(os.Getenv("SYNC_INTERVAL_SECONDS")); err == nil && v > 0 {
		syncIntervalSeconds = v
	}
	syncInterval := time.Duration(syncIntervalSeconds) * time.Second

	// --- Open DB ---
	sqlDB, err := db.Open(dbURL)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	// --- Derive crypto key ---
	cryptoKey, err := cryptoModule.DeriveKey(secretKey)
	if err != nil {
		slog.Error("failed to derive crypto key", "err", err)
		os.Exit(1)
	}

	// --- Create clone dir ---
	if err := os.MkdirAll(cloneDir, 0755); err != nil {
		slog.Error("failed to create clone dir", "dir", cloneDir, "err", err)
		os.Exit(1)
	}

	// --- State store ---
	store := state.New()
	if strings.ToLower(os.Getenv("DEV_SEED")) == "true" {
		seedDevState(store)
	}
	store.SetInfisical(state.InfisicalState{
		Enabled: strings.ToLower(os.Getenv("INFISICAL_ENABLED")) == "true",
		Env:     os.Getenv("INFISICAL_ENV"),
	})

	// --- Docker client ---
	var dockerClient *docker.Client
	if c, err := docker.New(); err == nil {
		dockerClient = c
	} else {
		slog.Warn("docker client unavailable, container details and log streaming disabled", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	// --- Startup stack scan ---
	startupRepos, err := db.ListRepos(ctx, sqlDB)
	if err != nil {
		slog.Error("failed to list repos from DB", "err", err)
	} else {
		for _, repo := range startupRepos {
			if !repo.Enabled {
				continue
			}
			syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, syncInterval, store, dockerClient)
		}
		refreshContainers(ctx, store, &dockerClient)
	}

	// --- Dashboard server (always enabled) ---
	syncTrigger := make(chan string, 16)
	srv := server.New(store, dockerClient, syncTrigger, port, sqlDB, cryptoKey)
	go srv.Start(ctx)

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	doSyncRound := func(repos []db.RepoDB) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, repo := range repos {
				if ctx.Err() != nil {
					return
				}
				if !repo.Enabled {
					continue
				}
				if shouldSkipSync(repo.Name) {
					slog.Info("skipping sync (backoff)", "repo", repo.Name)
					continue
				}
				syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, syncInterval, store, dockerClient)
			}
			refreshContainers(ctx, store, &dockerClient)
		}()
		wg.Wait()
	}

	for {
		listCtx, listCancel := context.WithTimeout(ctx, 10*time.Second)
		repos, listErr := db.ListRepos(listCtx, sqlDB)
		listCancel()
		if listErr != nil {
			slog.Error("failed to list repos", "err", listErr)
			repos = nil
		}

		doSyncRound(repos)

		select {
		case <-ctx.Done():
			slog.Info("shutdown signal received, waiting for in-flight operations to complete")
			ticker.Stop()
			waitDone := make(chan struct{})
			go func() { wg.Wait(); close(waitDone) }()
			select {
			case <-waitDone:
				slog.Info("all operations completed, shutting down cleanly")
			case <-time.After(30 * time.Second):
				slog.Warn("shutdown timeout reached, forcing exit")
			}
			return

		case <-ticker.C:
			// regular interval — loop back to sync all repos

		case repoName := <-syncTrigger:
		drainLoop:
			for {
				select {
				case <-syncTrigger:
				default:
					break drainLoop
				}
			}
			slog.Info("manual sync triggered", "repo", repoName)
			resetBackoff(repoName)
			for _, repo := range repos {
				if repo.Name == repoName {
					syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, syncInterval, store, dockerClient)
					break
				}
			}
			refreshContainers(ctx, store, &dockerClient)
			ticker.Reset(syncInterval)
		}
	}
}


func seedDevState(store *state.Store) {
now := time.Now()
store.UpdateRepo(state.RepoState{
Name:     "dockers",
LastSync: now.Add(-3 * time.Minute),
LastSHA:  "a3f9c12",
Status:   state.StatusOK,
})
store.UpdateRepo(state.RepoState{
Name:     "cluster",
LastSync: now.Add(-12 * time.Minute),
LastSHA:  "b8e2d47",
Status:   state.StatusError,
LastError: "git pull: exit status 1",
})
store.UpdateStack(state.StackState{
Name: "monitorss", RepoName: "dockers",
LastApply: now.Add(-3 * time.Minute), Status: state.ApplyOK,
})
store.UpdateStack(state.StackState{
Name: "plex", RepoName: "dockers",
LastApply: now.Add(-15 * time.Minute), Status: state.ApplyOK,
})
store.UpdateStack(state.StackState{
Name: "litellm", RepoName: "dockers",
LastApply: now.Add(-1 * time.Minute), Status: state.ApplyApplying,
})
store.UpdateStack(state.StackState{
Name: "redis", RepoName: "dockers",
LastApply: now.Add(-30 * time.Minute), Status: state.ApplyError,
LastError: "image pull failed: rate limit exceeded",
})
store.SetInfisical(state.InfisicalState{Enabled: true, Env: "prod"})
}
