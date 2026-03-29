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
	"regexp"
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

// InfisicalConfig holds Infisical credentials loaded from DB settings.
type InfisicalConfig struct {
	Token     string
	Env       string
	URL       string
	ProjectID string
}

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

// ansiEscape strips ANSI colour/control escape sequences from s.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[mGKHF]`)

// extractErrorSummary returns the last meaningful lines from command output
// when a process exits non-zero, so callers see something useful rather than
// just "exit status 1".
func extractErrorSummary(output string, fallback string) string {
	clean := ansiEscape.ReplaceAllString(output, "")
	var lines []string
	for _, l := range strings.Split(clean, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return fallback
	}
	// Return the last 5 meaningful lines — usually where errors appear.
	const maxLines = 5
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}


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
// composeUsesEnvVars returns true if the compose file in stackPath contains
// any ${VAR} or $VAR substitution references, meaning Infisical will actually
// inject something useful. Stacks with no variable references get no indicator
// even when a global token is configured.
func composeUsesEnvVars(stackPath string) bool {
	candidates := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for _, name := range candidates {
		data, err := os.ReadFile(filepath.Join(stackPath, name))
		if err != nil {
			continue
		}
		content := string(data)
		// Match ${VAR...} or bare $WORD (uppercase env var convention)
		if strings.Contains(content, "${") {
			return true
		}
		// Also catch bare $UPPERCASE_VAR patterns
		for i := 0; i < len(content)-1; i++ {
			if content[i] == '$' && content[i+1] >= 'A' && content[i+1] <= 'Z' {
				return true
			}
		}
		return false // found the file, no vars
	}
	return false
}

func infisicalMode(stackPath string, cfg InfisicalConfig) string {
	tomlPath := filepath.Join(stackPath, "infisical.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return "per-stack"
	}
	if cfg.Token != "" && composeUsesEnvVars(stackPath) {
		return "global"
	}
	return ""
}

func buildComposeCmd(ctx context.Context, stackPath, stackName string, cfg InfisicalConfig) *exec.Cmd {
	tomlPath := filepath.Join(stackPath, "infisical.toml")
	_, tomlErr := os.Stat(tomlPath)
	hasToml := tomlErr == nil

	if !hasToml && cfg.Token == "" {
		slog.Info("applying stack", "stack", stackName, "infisical", false)
		return exec.CommandContext(ctx, "docker", "compose", "up", "-d")
	}

	if hasToml {
		args := []string{"run", "--config=" + tomlPath}
		if cfg.URL != "" {
			args = append(args, "--domain="+cfg.URL)
		}
		args = append(args, "--", "docker", "compose", "up", "-d")
		slog.Info("stack using per-stack infisical.toml", "stack", stackName)
		return exec.CommandContext(ctx, "infisical", args...)
	}

	args := []string{"run", "--token=" + cfg.Token, "--env=" + cfg.Env}
	if cfg.ProjectID != "" {
		args = append(args, "--projectId="+cfg.ProjectID)
	}
	if cfg.URL != "" {
		args = append(args, "--domain="+cfg.URL)
	}
	args = append(args, "--", "docker", "compose", "up", "-d")
	slog.Info("stack using global infisical token", "stack", stackName, "env", cfg.Env)
	return exec.CommandContext(ctx, "infisical", args...)
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


func applyStack(ctx context.Context, stackPath, stackName, repoName string, store *state.Store, dockerClient *docker.Client, infisicalCfg InfisicalConfig, bus *state.ActivityBus) {
if bus != nil {
	bus.Publish(state.ActivityEvent{Type: "applying", Repo: repoName, Stack: stackName, Msg: "Applying " + stackName})
}
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
cmd := buildComposeCmd(applyCtx, stackPath, stackName, infisicalCfg)
cmd.Dir = stackPath
output, err := cmd.CombinedOutput()
outputStr := string(output)
if len(output) > 0 {
slog.Info("stack compose output", "stack", stackName, "output", outputStr)
}

if store != nil {
st := state.StackState{
Name:          stackName,
RepoName:      repoName,
StackDir:      stackPath,
LastApply:     time.Now(),
LastOutput:    outputStr,
Containers:    []state.ContainerDetail{},
InfisicalMode: infisicalMode(stackPath, infisicalCfg),
}
if err != nil {
st.Status = state.ApplyError
st.LastError = extractErrorSummary(outputStr, err.Error())
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
if bus != nil {
	bus.Publish(state.ActivityEvent{Type: "error", Repo: repoName, Stack: stackName, Msg: stackName + " failed"})
}
} else {
slog.Info("stack applied successfully", "stack", stackName)
if bus != nil {
	bus.Publish(state.ActivityEvent{Type: "done", Repo: repoName, Stack: stackName, Msg: stackName + " ready"})
}
}
}


// runStacksSync discovers and applies all docker-compose stacks in stacksDir.
func runStacksSync(ctx context.Context, stacksDir, repoName string, store *state.Store, dockerClient *docker.Client, infisicalCfg InfisicalConfig, bus *state.ActivityBus) {
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
		applyStack(ctx, stackPath, stackName, repoName, store, dockerClient, infisicalCfg, bus)
	}
}

// syncRepoFromDB clones or pulls the repo and applies stacks if the SHA changed.
func syncRepoFromDB(ctx context.Context, repo db.RepoDB, cloneDir string, cryptoKey []byte, sqlDB *sql.DB, store *state.Store, dockerClient *docker.Client, infisicalCfg InfisicalConfig, bus *state.ActivityBus) {
	repoName := repo.Name
	start := time.Now()

	syncInterval := time.Duration(repo.SyncInterval) * time.Second
	if syncInterval <= 0 {
		syncInterval = 60 * time.Second
	}

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
			defer os.Remove(keyPath)
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

	// Use the state store's last known SHA (not git on disk) so that after a
	// restart the in-memory state is empty and stacks always get re-applied.
	var oldSHA string
	if store != nil {
		if rs, ok := store.GetRepo(repoName); ok {
			oldSHA = rs.LastSHA
		}
	}

	cloneCtx, cloneCancel := context.WithTimeout(ctx, 120*time.Second)
	if bus != nil {
		bus.Publish(state.ActivityEvent{Type: "pulling", Repo: repoName, Msg: "Pulling " + repoName})
	}
	err := git.Clone(cloneCtx, repo.URL, destDir, opts)
	cloneCancel()
	if err != nil {
		if bus != nil {
			bus.Publish(state.ActivityEvent{Type: "error", Repo: repoName, Msg: repoName + " pull failed"})
		}
		recordError(fmt.Sprintf("git clone/pull failed: %v", err))
		return
	}

	newSHACtx, newSHACancel := context.WithTimeout(ctx, 10*time.Second)
	newSHA, _ := git.HeadSHA(newSHACtx, destDir)
	newSHACancel()

	if newSHA != oldSHA || oldSHA == "" {
		stacksDir := filepath.Join(destDir, repo.StacksDir)
		runStacksSync(ctx, stacksDir, repoName, store, dockerClient, infisicalCfg, bus)
	}
	if bus != nil {
		bus.Publish(state.ActivityEvent{Type: "done", Repo: repoName, Msg: repoName + " up to date"})
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

func loadInfisicalFromDB(ctx context.Context, sqlDB *sql.DB, cryptoKey []byte) InfisicalConfig {
	cfg := InfisicalConfig{Env: "prod"}
	settings, err := db.GetAllSettings(ctx, sqlDB)
	if err != nil {
		slog.Warn("failed to load settings from DB", "err", err)
		return cfg
	}
	if v := settings["infisical_env"]; v != "" {
		cfg.Env = v
	}
	cfg.URL = settings["infisical_url"]
	cfg.ProjectID = settings["infisical_project_id"]
	// GetAllSettings masks sensitive values; fetch token separately
	tokenEnc, _, err := db.GetSetting(ctx, sqlDB, "infisical_token")
	if err == nil && tokenEnc != "" {
		if token, err := cryptoModule.Decrypt(cryptoKey, tokenEnc); err == nil {
			cfg.Token = token
		}
	}
	return cfg
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

	syncInterval := 60 * time.Second

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
	{
		initSettingsCtx, initSettingsCancel := context.WithTimeout(context.Background(), 5*time.Second)
		infCfg := loadInfisicalFromDB(initSettingsCtx, sqlDB, cryptoKey)
		initSettingsCancel()
		store.SetInfisical(state.InfisicalState{Enabled: infCfg.Token != "", Env: infCfg.Env})
	}

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

	// Create activity bus early so startup syncs emit events.
	activityBus := state.NewActivityBus()

	// --- Startup stack scan ---
	startupRepos, err := db.ListRepos(ctx, sqlDB)
	if err != nil {
		slog.Error("failed to list repos from DB", "err", err)
	} else {
		startupSettingsCtx, startupSettingsCancel := context.WithTimeout(ctx, 5*time.Second)
		startupInfCfg := loadInfisicalFromDB(startupSettingsCtx, sqlDB, cryptoKey)
		startupSettingsCancel()
		for _, repo := range startupRepos {
			if !repo.Enabled {
				continue
			}
			syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, store, dockerClient, startupInfCfg, activityBus)
		}
		refreshContainers(ctx, store, &dockerClient)
	}

	// --- Dashboard server (always enabled) ---
	syncTrigger := make(chan string, 16)
	srv := server.New(store, dockerClient, syncTrigger, port, sqlDB, cryptoKey, activityBus)

	// Wire per-stack apply: look up stack in store, then call applyStack.
	srv.SetApplyStack(func(repoName, stackName string) {
		st, ok := store.GetStack(repoName, stackName)
		if !ok {
			return
		}
		settingsCtx, settingsCancel := context.WithTimeout(ctx, 5*time.Second)
		infCfg := loadInfisicalFromDB(settingsCtx, sqlDB, cryptoKey)
		settingsCancel()
		applyStack(ctx, st.StackDir, stackName, repoName, store, dockerClient, infCfg, activityBus)
		refreshContainers(ctx, store, &dockerClient)
	})

	go srv.Start(ctx)

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	doSyncRound := func(repos []db.RepoDB, infisicalCfg InfisicalConfig) {
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
				syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, store, dockerClient, infisicalCfg, activityBus)
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

		settingsCtx, settingsCancel := context.WithTimeout(ctx, 5*time.Second)
		infCfg := loadInfisicalFromDB(settingsCtx, sqlDB, cryptoKey)
		settingsCancel()
		store.SetInfisical(state.InfisicalState{Enabled: infCfg.Token != "", Env: infCfg.Env})

		doSyncRound(repos, infCfg)

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
			trigSettingsCtx, trigSettingsCancel := context.WithTimeout(ctx, 5*time.Second)
			trigInfCfg := loadInfisicalFromDB(trigSettingsCtx, sqlDB, cryptoKey)
			trigSettingsCancel()
			for _, repo := range repos {
				if repo.Name == repoName {
					syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, store, dockerClient, trigInfCfg, activityBus)
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
