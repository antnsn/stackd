package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
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

	"stackd/internal/compose"
	cryptoModule "stackd/internal/crypto"
	"stackd/internal/db"
	"stackd/internal/docker"
	"stackd/internal/execpg"
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

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

var repoBackoffs sync.Map // key: repo name (string), value: *syncBackoff
var repoLocks sync.Map    // key: repo name (string), value: *sync.Mutex
var repoNextDue sync.Map  // key: repo name (string), value: time.Time (next scheduled sync)

// sshTempDir is the private directory (0700) into which decrypted SSH private
// keys are written. main() points it at <cloneDir>/.ssh so keys never land in
// world-readable /tmp. Empty falls back to the OS temp dir.
var sshTempDir string

// repoLock returns the per-repo mutex, serialising all git work for a repo so
// concurrent syncs (scheduled + manual + per-stack apply) cannot race on the
// same clone directory or auth key file.
func repoLock(repoName string) *sync.Mutex {
	v, _ := repoLocks.LoadOrStore(repoName, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// dueForSync reports whether a repo's own sync interval has elapsed since it was
// last scheduled. Repos with a long interval are skipped on the frequent base
// ticker until they come due.
func dueForSync(repoName string) bool {
	v, ok := repoNextDue.Load(repoName)
	if !ok {
		return true
	}
	return !time.Now().Before(v.(time.Time))
}

// markScheduled records the next time a repo becomes due for a scheduled sync.
func markScheduled(repoName string, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	repoNextDue.Store(repoName, time.Now().Add(interval))
}

// effectiveInterval resolves a repo's sync interval: its own SyncInterval, then
// the default_sync_interval setting, then 60s.
func effectiveInterval(repo db.RepoDB, defaultInterval time.Duration) time.Duration {
	if repo.SyncInterval > 0 {
		return time.Duration(repo.SyncInterval) * time.Second
	}
	if defaultInterval > 0 {
		return defaultInterval
	}
	return 60 * time.Second
}

// loadDefaultInterval reads the default_sync_interval setting (seconds).
func loadDefaultInterval(ctx context.Context, sqlDB *sql.DB) time.Duration {
	if val, _, err := db.GetSetting(ctx, sqlDB, "default_sync_interval"); err == nil && val != "" {
		if n, perr := strconv.Atoi(val); perr == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// safeRepoPath joins cloneDir and repoName and verifies the result stays within
// cloneDir (defence-in-depth against a malicious repo Name escaping via "..").
func safeRepoPath(cloneDir, repoName string) (string, error) {
	if err := git.ValidateRepoName(repoName); err != nil {
		return "", err
	}
	dest := filepath.Join(cloneDir, repoName)
	rel, err := filepath.Rel(cloneDir, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repo path %q escapes clone dir", repoName)
	}
	return dest, nil
}

// setupGitAuth decrypts the SSH key / PAT for a repo and returns AuthOpts plus a
// cleanup func that removes any temporary key file. The key is written with
// os.CreateTemp (unique path) so concurrent repos never collide on a fixed path.
func setupGitAuth(ctx context.Context, repo db.RepoDB, cryptoKey []byte, sqlDB *sql.DB) (git.AuthOpts, func(), error) {
	opts := git.AuthOpts{Type: repo.AuthType}
	cleanup := func() {}
	switch repo.AuthType {
	case "ssh":
		if repo.SSHKeyID != "" {
			keyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			sshKey, err := db.GetSSHKey(keyCtx, sqlDB, repo.SSHKeyID)
			cancel()
			if err != nil {
				return opts, cleanup, fmt.Errorf("get SSH key: %w", err)
			}
			privKey, err := cryptoModule.Decrypt(cryptoKey, sshKey.PrivateKeyEnc)
			if err != nil {
				return opts, cleanup, fmt.Errorf("decrypt SSH key: %w", err)
			}
			// OpenSSH rejects a private key that lacks a trailing newline while
			// gossh.ParsePrivateKey (used at create time) accepts it, so a key
			// that validated on save would fail every git sync. Ensure exactly one.
			if !strings.HasSuffix(privKey, "\n") {
				privKey += "\n"
			}
			// Write the key into the private sshTempDir (0700) rather than the
			// world-readable OS temp dir.
			f, err := os.CreateTemp(sshTempDir, "stackd-key-*")
			if err != nil {
				return opts, cleanup, fmt.Errorf("create temp key file: %w", err)
			}
			path := f.Name()
			if _, err := f.WriteString(privKey); err != nil {
				f.Close()
				os.Remove(path)
				return opts, cleanup, fmt.Errorf("write SSH key file: %w", err)
			}
			f.Close()
			cleanup = func() { os.Remove(path) }
			opts.SSHKeyPath = path
		}
	case "pat":
		if repo.PATEnc != "" {
			pat, err := cryptoModule.Decrypt(cryptoKey, repo.PATEnc)
			if err != nil {
				return opts, cleanup, fmt.Errorf("decrypt PAT: %w", err)
			}
			opts.PAT = pat
		}
	}
	return opts, cleanup, nil
}

// gitSyncToDisk sets up auth and brings the repo's clone up to date (fetch+reset
// or clone). It returns the destination directory. Callers MUST already hold the
// per-repo lock. Shared by scheduled syncs and the per-stack apply callback.
func gitSyncToDisk(ctx context.Context, repo db.RepoDB, cloneDir string, cryptoKey []byte, sqlDB *sql.DB) (string, error) {
	destDir, err := safeRepoPath(cloneDir, repo.Name)
	if err != nil {
		return "", err
	}
	opts, cleanup, err := setupGitAuth(ctx, repo, cryptoKey, sqlDB)
	if err != nil {
		return destDir, err
	}
	defer cleanup()

	remote := repo.Remote
	if remote == "" {
		remote = "origin"
	}
	syncCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := git.SyncRepo(syncCtx, destDir, repo.URL, remote, repo.Branch, opts); err != nil {
		return destDir, err
	}
	return destDir, nil
}

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
// Infisical secrets injection is applied automatically when credentials are
// available. Auth priority:
//  1. Per-stack infisical.toml in the stack directory (--config=<path>)
//  2. A global Infisical token configured in settings (InfisicalConfig.Token)
//
// When neither a token nor a toml is present, the stack is applied without
// secrets injection. InfisicalConfig.URL can point to a self-hosted instance.

// buildComposeCmd constructs the exec.Cmd to apply a stack. It returns either a bare
// "docker compose up -d" or an "infisical run -- docker compose up -d" command depending
// on the INFISICAL_ENABLED env var and available credentials. The provided ctx is used
// directly, so callers should set an appropriate deadline before calling.
// composeUsesEnvVars returns true if the compose file in stackPath contains
// any ${VAR} or $VAR substitution references, meaning Infisical will actually
// inject something useful. Stacks with no variable references get no indicator
// even when a global token is configured.
func composeUsesEnvVars(stackPath string) bool {
	for _, name := range compose.Candidates {
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
		return execpg.CommandContext(ctx, "docker", "compose", "up", "-d")
	}

	if hasToml {
		args := []string{"run", "--config=" + tomlPath}
		if cfg.URL != "" {
			args = append(args, "--domain="+cfg.URL)
		}
		args = append(args, "--", "docker", "compose", "up", "-d")
		slog.Info("stack using per-stack infisical.toml", "stack", stackName)
		return execpg.CommandContext(ctx, "infisical", args...)
	}

	// Pass the token via INFISICAL_TOKEN in the environment rather than
	// --token= on argv, so it never appears in the process command line (which
	// is readable by any local user via ps/procfs).
	args := []string{"run", "--env=" + cfg.Env}
	if cfg.ProjectID != "" {
		args = append(args, "--projectId="+cfg.ProjectID)
	}
	if cfg.URL != "" {
		args = append(args, "--domain="+cfg.URL)
	}
	args = append(args, "--", "docker", "compose", "up", "-d")
	slog.Info("stack using global infisical token", "stack", stackName, "env", cfg.Env)
	cmd := execpg.CommandContext(ctx, "infisical", args...)
	cmd.Env = append(os.Environ(), "INFISICAL_TOKEN="+cfg.Token)
	return cmd
}

// refreshContainers updates container details for all stacks whose StackDir is
// known. Safe to call with a nil holder (no-op). Attempts reconnection when the
// holder currently has no client, publishing the reconnected client through the
// holder so the HTTP server sees it too.
func refreshContainers(ctx context.Context, store *state.Store, holder *docker.ClientHolder) {
	if holder == nil {
		return
	}
	client := holder.Get()
	if client == nil {
		c, err := docker.New()
		if err != nil {
			slog.Warn("docker reconnection failed", "err", err)
			return
		}
		// Publish atomically: if a concurrent caller already reconnected, we lose
		// the race and must Close our client so it is not leaked.
		if holder.CompareAndSwap(nil, c) {
			slog.Info("docker client reconnected")
			client = c
		} else {
			_ = c.Close()
			client = holder.Get()
			if client == nil {
				return
			}
		}
	}
	for _, st := range store.GetAllStacks() {
		if st.StackDir == "" {
			continue
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ctrs, err := client.ListStackContainerDetails(refreshCtx, st.StackDir)
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

func applyStack(ctx context.Context, stackPath, stackName, repoName string, store *state.Store, holder *docker.ClientHolder, infisicalCfg InfisicalConfig, bus *state.ActivityBus) {
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
			if dockerClient := holder.Get(); dockerClient != nil {
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
func runStacksSync(ctx context.Context, stacksDir, repoName string, store *state.Store, holder *docker.ClientHolder, infisicalCfg InfisicalConfig, bus *state.ActivityBus) {
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
		for _, candidate := range compose.Candidates {
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
		applyStack(ctx, stackPath, stackName, repoName, store, holder, infisicalCfg, bus)
	}
}

// syncRepoFromDB brings the repo's clone up to date and applies stacks if the
// SHA changed. It acquires the per-repo lock so concurrent syncs (scheduled,
// manual, per-stack apply) are serialised.
func syncRepoFromDB(ctx context.Context, repo db.RepoDB, cloneDir string, cryptoKey []byte, sqlDB *sql.DB, store *state.Store, holder *docker.ClientHolder, infisicalCfg InfisicalConfig, bus *state.ActivityBus, defaultInterval time.Duration) {
	repoName := repo.Name
	start := time.Now()

	// defaultInterval is resolved once by the caller (per sync round / startup)
	// so there is no unbounded per-repo DB query in the sync path.
	syncInterval := effectiveInterval(repo, defaultInterval)

	if store != nil {
		existing, ok := store.GetRepo(repoName)
		if !ok {
			existing = state.RepoState{Name: repoName}
		}
		existing.Status = state.StatusSyncing
		store.UpdateRepo(existing)
	}

	recordError := func(msg string) {
		msg = git.Redact(msg)
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

	mu := repoLock(repoName)
	mu.Lock()
	defer mu.Unlock()

	// Use the state store's last known SHA (not git on disk) so that after a
	// restart the in-memory state is empty and stacks always get re-applied.
	var oldSHA string
	if store != nil {
		if rs, ok := store.GetRepo(repoName); ok {
			oldSHA = rs.LastSHA
		}
	}

	if bus != nil {
		bus.Publish(state.ActivityEvent{Type: "pulling", Repo: repoName, Msg: "Pulling " + repoName})
	}
	destDir, err := gitSyncToDisk(ctx, repo, cloneDir, cryptoKey, sqlDB)
	if err != nil {
		if bus != nil {
			bus.Publish(state.ActivityEvent{Type: "error", Repo: repoName, Msg: repoName + " pull failed"})
		}
		recordError(fmt.Sprintf("git sync failed: %v", err))
		return
	}

	newSHACtx, newSHACancel := context.WithTimeout(ctx, 10*time.Second)
	newSHA, shaErr := git.HeadSHA(newSHACtx, destDir)
	newSHACancel()
	if shaErr != nil {
		if bus != nil {
			bus.Publish(state.ActivityEvent{Type: "error", Repo: repoName, Msg: repoName + " sync failed"})
		}
		recordError(fmt.Sprintf("resolve HEAD SHA: %v", shaErr))
		return
	}

	// A non-empty oldSHA that differs re-applies; an empty oldSHA (fresh start)
	// always differs from a real SHA, so it is covered by newSHA != oldSHA.
	if newSHA != oldSHA {
		stacksDir := filepath.Join(destDir, repo.StacksDir)
		runStacksSync(ctx, stacksDir, repoName, store, holder, infisicalCfg, bus)
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
	markScheduled(repoName, syncInterval)
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
	// SECRET_KEY feeds HKDF, which does no work-factor stretching, so a
	// high-entropy key is required. Warn (do not fail) on short keys: a hard
	// error would brick existing deployments on upgrade, and changing the
	// derivation itself would invalidate all existing ciphertext.
	if len(secretKey) < 16 {
		slog.Warn("SECRET_KEY is shorter than 16 characters; use a high-entropy random value of at least 16 characters", "length", len(secretKey))
	}
	cloneDir := os.Getenv("CLONE_DIR")
	if cloneDir == "" {
		cloneDir = "/var/lib/stackd/repos"
	}
	// Bind to all interfaces by default so published container ports work out of
	// the box (Docker forwards to the container's network address, not loopback).
	// Access is already gated by the mandatory dashboard token (auth is always on
	// via ensureDashboardToken). Set BIND_ADDR=127.0.0.1 to harden a host install.
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}

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

	// --- Private SSH dir ---
	// Keep the managed known_hosts and decrypted private keys out of the
	// world-writable /tmp default. Unless the operator pinned SSH_KNOWN_HOSTS_DIR
	// explicitly, point both git's known_hosts (via the env var read in
	// internal/git) and our key temp dir at a private <cloneDir>/.ssh (0700).
	sshDir := os.Getenv("SSH_KNOWN_HOSTS_DIR")
	if sshDir == "" {
		sshDir = filepath.Join(cloneDir, ".ssh")
		if err := os.Setenv("SSH_KNOWN_HOSTS_DIR", sshDir); err != nil {
			slog.Error("failed to set SSH_KNOWN_HOSTS_DIR", "err", err)
			os.Exit(1)
		}
	}
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		slog.Error("failed to create ssh dir", "dir", sshDir, "err", err)
		os.Exit(1)
	}
	sshTempDir = sshDir

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
	// Held in a ClientHolder so reconnects performed by refreshContainers become
	// visible to the HTTP server without a data race.
	var initialClient *docker.Client
	if c, err := docker.New(); err == nil {
		initialClient = c
	} else {
		slog.Warn("docker client unavailable, container details and log streaming disabled", "err", err)
	}
	dockerHolder := docker.NewHolder(initialClient)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Create activity bus early so startup syncs emit events.
	activityBus := state.NewActivityBus()

	// --- Startup stack scan ---
	startupRepos, err := db.ListRepos(ctx, sqlDB)
	if err != nil {
		slog.Error("failed to list repos from DB", "err", err)
	} else {
		startupSettingsCtx, startupSettingsCancel := context.WithTimeout(ctx, 5*time.Second)
		startupInfCfg := loadInfisicalFromDB(startupSettingsCtx, sqlDB, cryptoKey)
		startupDefaultInterval := loadDefaultInterval(startupSettingsCtx, sqlDB)
		startupSettingsCancel()
		for _, repo := range startupRepos {
			if !repo.Enabled {
				continue
			}
			syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, store, dockerHolder, startupInfCfg, activityBus, startupDefaultInterval)
		}
		refreshContainers(ctx, store, dockerHolder)
	}

	// --- Dashboard server (always enabled) ---
	syncTrigger := make(chan string, 16)
	srv := server.New(store, dockerHolder, syncTrigger, bindAddr, port, sqlDB, cryptoKey, activityBus)

	// Set the dashboard token, fail-closed: env var wins, then a persisted
	// token, otherwise generate a random one and persist it so auth is ALWAYS
	// enabled (an empty token would leave the API unauthenticated).
	if err := ensureDashboardToken(srv, sqlDB, cloneDir); err != nil {
		slog.Error("failed to initialise dashboard token", "err", err)
		os.Exit(1)
	}
	// Pass system info to server.
	dbPath := strings.TrimPrefix(dbURL, "sqlite://")
	srv.SetSystemInfo(Version, cloneDir, dbPath)

	// Wire per-stack apply: pull the repo (under the same per-repo lock used by
	// scheduled syncs, via the shared gitSyncToDisk helper) then apply the stack.
	srv.SetApplyStack(func(repoName, stackName string) {
		st, ok := store.GetStack(repoName, stackName)
		if !ok {
			return
		}
		repoCtx, repoCancel := context.WithTimeout(ctx, 10*time.Second)
		repoDB, err := db.GetRepoByName(repoCtx, sqlDB, repoName)
		repoCancel()
		if err != nil {
			slog.Error("applyStack: repo not found in db", "repo", repoName, "err", err)
			return
		}
		settingsCtx, settingsCancel := context.WithTimeout(ctx, 5*time.Second)
		infCfg := loadInfisicalFromDB(settingsCtx, sqlDB, cryptoKey)
		settingsCancel()

		if activityBus != nil {
			activityBus.Publish(state.ActivityEvent{Type: "pulling", Repo: repoName, Msg: "Pulling " + repoName})
		}
		// Serialise with scheduled syncs on the same per-repo lock.
		mu := repoLock(repoName)
		mu.Lock()
		_, gitErr := gitSyncToDisk(ctx, repoDB, cloneDir, cryptoKey, sqlDB)
		mu.Unlock()
		if gitErr != nil {
			slog.Error("applyStack: git sync failed", "repo", repoName, "err", git.Redact(gitErr.Error()))
			if activityBus != nil {
				activityBus.Publish(state.ActivityEvent{Type: "error", Repo: repoName, Msg: repoName + " pull failed"})
			}
		} else if activityBus != nil {
			activityBus.Publish(state.ActivityEvent{Type: "done", Repo: repoName, Msg: repoName + " up to date"})
		}

		applyStack(ctx, st.StackDir, stackName, repoName, store, dockerHolder, infCfg, activityBus)
		refreshContainers(ctx, store, dockerHolder)
	})

	go srv.Start(ctx)

	// A frequent base ticker drives scheduling checks; each repo is only synced
	// when its own interval has elapsed (see dueForSync / markScheduled).
	baseTick := 15 * time.Second
	ticker := time.NewTicker(baseTick)
	defer ticker.Stop()

	doSyncRound := func(repos []db.RepoDB, infisicalCfg InfisicalConfig, defaultInterval time.Duration) {
		for _, repo := range repos {
			if ctx.Err() != nil {
				return
			}
			if !repo.Enabled {
				continue
			}
			if shouldSkipSync(repo.Name) {
				slog.Debug("skipping sync (backoff)", "repo", repo.Name)
				continue
			}
			if !dueForSync(repo.Name) {
				continue
			}
			syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, store, dockerHolder, infisicalCfg, activityBus, defaultInterval)
		}
		refreshContainers(ctx, store, dockerHolder)
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
		defaultInterval := loadDefaultInterval(settingsCtx, sqlDB)
		settingsCancel()
		store.SetInfisical(state.InfisicalState{Enabled: infCfg.Token != "", Env: infCfg.Env})

		doSyncRound(repos, infCfg, defaultInterval)

		select {
		case <-ctx.Done():
			// doSyncRound runs synchronously and already returned, so there is no
			// in-flight sync to wait on here (each op respects ctx).
			slog.Info("shutdown signal received, shutting down cleanly")
			ticker.Stop()
			return

		case <-ticker.C:
			// base tick — re-evaluate which repos are due

		case repoName := <-syncTrigger:
			// Drain ALL pending triggers into a set so every requested repo is
			// synced this round, not just the first.
			triggered := map[string]struct{}{repoName: {}}
		drainLoop:
			for {
				select {
				case extra := <-syncTrigger:
					triggered[extra] = struct{}{}
				default:
					break drainLoop
				}
			}
			trigSettingsCtx, trigSettingsCancel := context.WithTimeout(ctx, 5*time.Second)
			trigInfCfg := loadInfisicalFromDB(trigSettingsCtx, sqlDB, cryptoKey)
			trigSettingsCancel()
			for _, repo := range repos {
				if _, ok := triggered[repo.Name]; !ok {
					continue
				}
				slog.Info("manual sync triggered", "repo", repo.Name)
				resetBackoff(repo.Name)
				syncRepoFromDB(ctx, repo, cloneDir, cryptoKey, sqlDB, store, dockerHolder, trigInfCfg, activityBus, defaultInterval)
			}
			refreshContainers(ctx, store, dockerHolder)
			ticker.Reset(baseTick)
		}
	}
}

// ensureDashboardToken sets the dashboard bearer token fail-closed:
//   - DASHBOARD_TOKEN env var takes precedence,
//   - otherwise a persisted dashboard_token is used,
//   - otherwise a random 32-byte token is generated, persisted, and written to
//     <cloneDir>/dashboard_token (0600) for the operator to retrieve.
//
// The generated token value is never logged (only the path to the file is), so
// the secret does not leak into stdout / log aggregation. API authentication is
// ALWAYS enabled.
func ensureDashboardToken(srv *server.Server, sqlDB *sql.DB, cloneDir string) error {
	if envToken := os.Getenv("DASHBOARD_TOKEN"); envToken != "" {
		srv.SetToken(envToken)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if dbToken, _, err := db.GetSetting(ctx, sqlDB, "dashboard_token"); err == nil && dbToken != "" {
		srv.SetToken(dbToken)
		return nil
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generate dashboard token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	// Write the retrieval file FIRST and only persist to the DB once the operator
	// has a way to read the token. Otherwise a failed file write would leave an
	// unknown token committed to the DB — on the next start it would be loaded and
	// the file-write path skipped, locking the operator out permanently. The token
	// value is never logged (only the path is), so it doesn't leak to log stores.
	tokenPath := filepath.Join(cloneDir, "dashboard_token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		return fmt.Errorf("write dashboard token file %s: %w", tokenPath, err)
	}
	if err := db.SetSetting(ctx, sqlDB, "dashboard_token", token); err != nil {
		return fmt.Errorf("persist dashboard token: %w", err)
	}
	srv.SetToken(token)
	slog.Warn("generated dashboard token — retrieve it from the token file or set DASHBOARD_TOKEN to override", "path", tokenPath)
	return nil
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
		Name:      "cluster",
		LastSync:  now.Add(-12 * time.Minute),
		LastSHA:   "b8e2d47",
		Status:    state.StatusError,
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
