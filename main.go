package main

import (
"context"
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

"gopkg.in/yaml.v3"

"stackd/internal/docker"
"stackd/internal/metrics"
"stackd/internal/server"
"stackd/internal/state"
)

// AppConfig holds global configuration for the stackd daemon.
type AppConfig struct {
PullOnly            bool
SyncIntervalSeconds int
SyncInterval        time.Duration
GitUserName         string
GitUserEmail        string
ConfigFile          string // path to optional stackd.yaml; empty = not used
}

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

// RepoConfig holds per-repository configuration, derived from env vars or stackd.yaml.
type RepoConfig struct {
Name         string // basename of the repo directory
Dir          string // absolute path to the git repo
StacksDir    string // absolute path to compose stacks for this repo
Branch       string // git branch to track, default "main"
Remote       string // git remote name, default "origin"
PostSyncCmd  string // optional shell command to run after pull
PullOnly     bool   // per-repo override; if true, never push
InfisicalEnv string // per-repo Infisical environment override
}

// yamlRepoConfig is the per-repo schema in stackd.yaml.
type yamlRepoConfig struct {
Name         string `yaml:"name"`
Dir          string `yaml:"dir"`
StacksDir    string `yaml:"stacksDir"`
Branch       string `yaml:"branch"`
Remote       string `yaml:"remote"`
PostSyncCmd  string `yaml:"postSyncCmd"`
PullOnly     bool   `yaml:"pullOnly"`
InfisicalEnv string `yaml:"infisicalEnv"`
}

// yamlAppConfig is the top-level schema of stackd.yaml.
type yamlAppConfig struct {
PullOnly            bool   `yaml:"pullOnly"`
SyncIntervalSeconds int    `yaml:"syncIntervalSeconds"`
GitUser             struct {
Name  string `yaml:"name"`
Email string `yaml:"email"`
} `yaml:"gitUser"`
Repos []yamlRepoConfig `yaml:"repos"`
}

// getMountedVolumes returns a list of directories inside REPOS_DIR (default /repos).
func getMountedVolumes() ([]string, error) {
reposDir := os.Getenv("REPOS_DIR")
if reposDir == "" {
reposDir = "/repos"
}
var volumes []string
files, err := os.ReadDir(reposDir)
if err != nil {
return nil, err
}
for _, file := range files {
if file.IsDir() {
volumes = append(volumes, filepath.Join(reposDir, file.Name()))
}
}
return volumes, nil
}

func setupSSH() error {
sshKeyPath := os.Getenv("SSH_KEY_PATH")
if sshKeyPath == "" {
sshKeyPath = "/root/.ssh/id_rsa"
}

if _, err := os.Stat(sshKeyPath); os.IsNotExist(err) {
return fmt.Errorf("SSH key not found at %s", sshKeyPath)
}

// Write SSH config and known_hosts to a private temp dir owned by this
// process. This avoids permission errors when SSH_KEY_PATH points to a
// bind-mounted directory owned by a different user (e.g. the host's plecto
// user vs. the container's root).
sshTmpDir := "/tmp/stackd-ssh"
if err := os.MkdirAll(sshTmpDir, 0700); err != nil {
return fmt.Errorf("failed to create ssh tmp dir: %v", err)
}

// Scan GitHub host keys.
sshCtx, sshCancel := context.WithTimeout(context.Background(), 30*time.Second)
defer sshCancel()
knownHostsCmd := exec.CommandContext(sshCtx, "ssh-keyscan", "github.com")
knownHosts, err := knownHostsCmd.Output()
if err != nil {
return fmt.Errorf("failed to scan GitHub SSH keys: %v", err)
}
knownHostsPath := filepath.Join(sshTmpDir, "known_hosts")
if err := os.WriteFile(knownHostsPath, knownHosts, 0600); err != nil {
return fmt.Errorf("failed to write known_hosts: %v", err)
}

// Write a minimal SSH config pointing at the user-supplied key.
configPath := filepath.Join(sshTmpDir, "config")
config := fmt.Sprintf(
"Host github.com\n\tIdentityFile %s\n\tUserKnownHostsFile %s\n\tStrictHostKeyChecking no\n",
sshKeyPath, knownHostsPath,
)
if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
return fmt.Errorf("failed to write SSH config: %v", err)
}
slog.Info("SSH config written", "path", configPath)

// Tell git (and any child ssh process) to use our private config.
os.Setenv("GIT_SSH_COMMAND", fmt.Sprintf("ssh -F %s", configPath))
return nil
}

func setGitIdentity(ctx context.Context, repoDir, userName, userEmail string) {
nameCtx, nameCancel := context.WithTimeout(ctx, 10*time.Second)
defer nameCancel()
nameCmd := exec.CommandContext(nameCtx, "git", "config", "user.name", userName)
nameCmd.Dir = repoDir
if err := nameCmd.Run(); err != nil {
slog.Warn("failed to set git user.name", "dir", repoDir, "err", err)
}

emailCtx, emailCancel := context.WithTimeout(ctx, 10*time.Second)
defer emailCancel()
emailCmd := exec.CommandContext(emailCtx, "git", "config", "user.email", userEmail)
emailCmd.Dir = repoDir
if err := emailCmd.Run(); err != nil {
slog.Warn("failed to set git user.email", "dir", repoDir, "err", err)
}
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

// loadAppConfig reads global configuration from environment variables.
func loadAppConfig() AppConfig {
pullOnly := strings.ToLower(os.Getenv("PULL_ONLY")) == "true"

gitUserName := os.Getenv("GIT_USER_NAME")
if gitUserName == "" {
gitUserName = "githubSync"
}

gitUserEmail := os.Getenv("GIT_USER_EMAIL")
if gitUserEmail == "" {
gitUserEmail = "githubsync@localhost"
}

syncIntervalSeconds := 60
if v, err := strconv.Atoi(os.Getenv("SYNC_INTERVAL_SECONDS")); err == nil && v > 0 {
syncIntervalSeconds = v
}

return AppConfig{
PullOnly:            pullOnly,
SyncIntervalSeconds: syncIntervalSeconds,
SyncInterval:        time.Duration(syncIntervalSeconds) * time.Second,
GitUserName:         gitUserName,
GitUserEmail:        gitUserEmail,
ConfigFile:          os.Getenv("STACKD_CONFIG"),
}
}

// loadYAMLConfig reads the optional stackd.yaml config file and returns a map
// of repo name → yamlRepoConfig. Returns an empty map if the file doesn't exist
// or configPath is empty.
func loadYAMLConfig(configPath string) map[string]yamlRepoConfig {
result := make(map[string]yamlRepoConfig)

if configPath == "" {
reposDir := os.Getenv("REPOS_DIR")
if reposDir == "" {
reposDir = "/repos"
}
configPath = filepath.Join(reposDir, "stackd.yaml")
}

data, err := os.ReadFile(configPath)
if err != nil {
return result
}

var cfg yamlAppConfig
if err := yaml.Unmarshal(data, &cfg); err != nil {
slog.Warn("failed to parse config", "path", configPath, "err", err)
return result
}

slog.Info("loaded config", "path", configPath, "repos", len(cfg.Repos))
for _, r := range cfg.Repos {
result[r.Name] = r
}
return result
}

// loadRepoConfigs discovers mounted repos and builds a RepoConfig for each one,
// merging env vars and optional YAML file configuration.
func loadRepoConfigs(appCfg AppConfig) ([]RepoConfig, error) {
dirs, err := getMountedVolumes()
if err != nil {
return nil, err
}

yamlCfgs := loadYAMLConfig(appCfg.ConfigFile)

defaultBranch := os.Getenv("BRANCH_DEFAULT")
if defaultBranch == "" {
defaultBranch = "main"
}

configs := make([]RepoConfig, 0, len(dirs))
for _, dir := range dirs {
name := filepath.Base(dir)
upper := strings.ToUpper(name)

cfg := RepoConfig{
Name:   name,
Dir:    dir,
Branch: defaultBranch,
Remote: "origin",
}

if yc, ok := yamlCfgs[name]; ok {
if yc.Branch != "" {
cfg.Branch = yc.Branch
}
if yc.Remote != "" {
cfg.Remote = yc.Remote
}
if yc.StacksDir != "" {
cfg.StacksDir = yc.StacksDir
}
if yc.PostSyncCmd != "" {
cfg.PostSyncCmd = yc.PostSyncCmd
}
if yc.InfisicalEnv != "" {
cfg.InfisicalEnv = yc.InfisicalEnv
}
cfg.PullOnly = yc.PullOnly
}

// Env vars override YAML (env > file > default)
if v := os.Getenv("BRANCH_" + upper); v != "" {
cfg.Branch = v
}
if v := os.Getenv("REMOTE_" + upper); v != "" {
cfg.Remote = v
}
if v := os.Getenv("STACKS_DIR_" + upper); v != "" {
cfg.StacksDir = v
}
if v := os.Getenv("POST_SYNC_" + upper); v != "" {
cfg.PostSyncCmd = v
}

configs = append(configs, cfg)
}
return configs, nil
}

func runStacksSync(ctx context.Context, cfg RepoConfig, store *state.Store, dockerClient *docker.Client) {
if cfg.StacksDir == "" {
return
}

entries, err := os.ReadDir(cfg.StacksDir)
if err != nil {
slog.Error("failed to read stacks dir", "repo", cfg.Name, "dir", cfg.StacksDir, "err", err)
return
}

for _, entry := range entries {
if !entry.IsDir() {
continue
}

stackName := entry.Name()
stackPath := filepath.Join(cfg.StacksDir, stackName)

composePath := ""
for _, candidate := range []string{"compose.yaml", "docker-compose.yml"} {
p := filepath.Join(stackPath, candidate)
if _, err := os.Stat(p); err == nil {
composePath = p
break
}
}

if composePath == "" {
slog.Warn("no compose file found, skipping stack", "stack", stackName, "repo", cfg.Name)
continue
}

applyStack(ctx, stackPath, stackName, cfg.Name, store, dockerClient)
}
}

func runPostSyncCommand(ctx context.Context, cfg RepoConfig) {
if cfg.PostSyncCmd == "" {
return
}

slog.Info("running post-sync command", "repo", cfg.Name, "cmd", cfg.PostSyncCmd)
postCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
defer cancel()
cmd := exec.CommandContext(postCtx, "sh", "-c", cfg.PostSyncCmd)
cmd.Dir = cfg.Dir
output, err := cmd.CombinedOutput()
if len(output) > 0 {
slog.Info("post-sync output", "repo", cfg.Name, "output", string(output))
}
if err != nil {
slog.Error("post-sync command failed", "repo", cfg.Name, "err", err)
} else {
slog.Info("post-sync command completed", "repo", cfg.Name)
}
}

func syncRepo(ctx context.Context, cfg RepoConfig, appCfg AppConfig, store *state.Store, dockerClient *docker.Client) {
repoName := cfg.Name
pullOnly := cfg.PullOnly || appCfg.PullOnly
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
recordSyncFailure(repoName, appCfg.SyncInterval)
	metrics.RecordSync(cfg.Name, "error", time.Since(start))
}

// Ensure only one sync runs per repo at a time.
lockVal, _ := repoLocks.LoadOrStore(cfg.Name, &sync.Mutex{})
repoMu := lockVal.(*sync.Mutex)
repoMu.Lock()
defer repoMu.Unlock()

// Mark the directory as safe for Git using --system instead of --global
safeCtx, safeCancel := context.WithTimeout(ctx, 10*time.Second)
configCmd := exec.CommandContext(safeCtx, "git", "config", "--system", "--add", "safe.directory", "*")
if err := configCmd.Run(); err != nil {
slog.Warn("failed to mark directories as safe", "err", err)
}
safeCancel()

fetchCtx, fetchCancel := context.WithTimeout(ctx, 120*time.Second)
cmd := exec.CommandContext(fetchCtx, "git", "fetch", cfg.Remote)
cmd.Dir = cfg.Dir
output, err := cmd.CombinedOutput()
fetchCancel()
if err != nil {
recordError(fmt.Sprintf("Failed to fetch in %s: %v. Output: %s", cfg.Dir, err, string(output)))
return
}

localCtx, localCancel := context.WithTimeout(ctx, 10*time.Second)
local := exec.CommandContext(localCtx, "git", "rev-parse", "@")
local.Dir = cfg.Dir
localSHA, err := local.Output()
localCancel()
if err != nil {
recordError(fmt.Sprintf("Failed to get local SHA in %s: %v", cfg.Dir, err))
return
}

remoteCtx, remoteCancel := context.WithTimeout(ctx, 10*time.Second)
remote := exec.CommandContext(remoteCtx, "git", "rev-parse", "@{u}")
remote.Dir = cfg.Dir
remoteSHA, err := remote.Output()
remoteCancel()
if err != nil {
recordError(fmt.Sprintf("Failed to get remote SHA in %s: %v", cfg.Dir, err))
return
}

currentSHA := strings.TrimSpace(string(localSHA))

if string(localSHA) != string(remoteSHA) {
slog.Info("remote changes detected, pulling", "repo", repoName, "branch", cfg.Branch, "remote", cfg.Remote)
pullCtx, pullCancel := context.WithTimeout(ctx, 120*time.Second)
pullCmd := exec.CommandContext(pullCtx, "git", "pull", cfg.Remote, cfg.Branch)
pullCmd.Dir = cfg.Dir
pullOut, pullErr := pullCmd.CombinedOutput()
pullCancel()
if pullErr != nil {
recordError(fmt.Sprintf("Failed to pull in %s: %v. Output: %s", cfg.Dir, pullErr, string(pullOut)))
return
}
// Update SHA after pull
shaCtx, shaCancel := context.WithTimeout(ctx, 10*time.Second)
if sha, err := exec.CommandContext(shaCtx, "git", "-C", cfg.Dir, "rev-parse", "HEAD").Output(); err == nil {
currentSHA = strings.TrimSpace(string(sha))
}
shaCancel()
runPostSyncCommand(ctx, cfg)
runStacksSync(ctx, cfg, store, dockerClient)
}

if pullOnly {
slog.Info("pull-only mode: skipping add/commit/push", "repo", repoName)
if store != nil {
store.UpdateRepo(state.RepoState{
Name:     repoName,
LastSync: time.Now(),
LastSHA:  currentSHA,
Status:   state.StatusOK,
})
}
recordSyncSuccess(repoName)
	metrics.RecordSync(cfg.Name, "success", time.Since(start))
return
}

// Set git identity before committing
setGitIdentity(ctx, cfg.Dir, appCfg.GitUserName, appCfg.GitUserEmail)

addCtx, addCancel := context.WithTimeout(ctx, 30*time.Second)
cmd = exec.CommandContext(addCtx, "git", "add", ".")
cmd.Dir = cfg.Dir
output, err = cmd.CombinedOutput()
addCancel()
if err != nil {
recordError(fmt.Sprintf("Failed to add changes in %s: %v. Output: %s", cfg.Dir, err, string(output)))
return
}

loc, err := time.LoadLocation(os.Getenv("TZ"))
if err != nil {
slog.Warn("failed to load timezone", "err", err)
loc = time.UTC
}
commitCtx, commitCancel := context.WithTimeout(ctx, 30*time.Second)
cmd = exec.CommandContext(commitCtx, "git", "commit", "-m", fmt.Sprintf("Automated commit %s", time.Now().In(loc).Format("2006-01-02 15:04:05")))
cmd.Dir = cfg.Dir
output, err = cmd.CombinedOutput()
commitCancel()
if err != nil {
slog.Info("no changes to commit", "repo", repoName, "err", err)
// Not a hard error — nothing to commit is normal.
if store != nil {
store.UpdateRepo(state.RepoState{
Name:     repoName,
LastSync: time.Now(),
LastSHA:  currentSHA,
Status:   state.StatusOK,
})
}
recordSyncSuccess(repoName)
	metrics.RecordSync(cfg.Name, "success", time.Since(start))
return
}

// Refresh SHA after commit
sha2Ctx, sha2Cancel := context.WithTimeout(ctx, 10*time.Second)
if sha, err := exec.CommandContext(sha2Ctx, "git", "-C", cfg.Dir, "rev-parse", "HEAD").Output(); err == nil {
currentSHA = strings.TrimSpace(string(sha))
}
sha2Cancel()

pushCtx, pushCancel := context.WithTimeout(ctx, 120*time.Second)
cmd = exec.CommandContext(pushCtx, "git", "push", cfg.Remote, cfg.Branch)
cmd.Dir = cfg.Dir
output, err = cmd.CombinedOutput()
pushCancel()
if err != nil {
recordError(fmt.Sprintf("Failed to push in %s: %v. Output: %s", cfg.Dir, err, string(output)))
return
}

slog.Info("successfully synced repo", "repo", repoName)
if store != nil {
store.UpdateRepo(state.RepoState{
Name:     repoName,
LastSync: time.Now(),
LastSHA:  currentSHA,
Status:   state.StatusOK,
})
}
recordSyncSuccess(repoName)
metrics.RecordSync(cfg.Name, "success", time.Since(start))
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

err := setupSSH()
if err != nil {
slog.Error("SSH setup failed", "err", err)
	os.Exit(1)
}

appCfg := loadAppConfig()

if appCfg.PullOnly {
slog.Info("pull-only mode enabled: skipping git add, commit, and push")
}

slog.Info("sync interval", "seconds", appCfg.SyncIntervalSeconds)

// --- State store ---
store := state.New()

if strings.ToLower(os.Getenv("DEV_SEED")) == "true" {
seedDevState(store)
}
store.SetInfisical(state.InfisicalState{
Enabled: strings.ToLower(os.Getenv("INFISICAL_ENABLED")) == "true",
Env:     os.Getenv("INFISICAL_ENV"),
})

// --- Docker client (used for container details and log streaming) ---
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
// Populate the state store with known stacks at startup so the dashboard
// shows containers immediately, even when no pull has occurred yet.
if repoCfgs, err := loadRepoConfigs(appCfg); err == nil {
for _, cfg := range repoCfgs {
runStacksSync(ctx, cfg, store, dockerClient)
}
refreshContainers(ctx, store, &dockerClient)
}

// --- Dashboard server ---
dashboardEnabled := strings.ToLower(os.Getenv("DASHBOARD_ENABLED")) == "true"
syncTrigger := make(chan string, 16)

if dashboardEnabled {
dashPort := 8080
if portStr := os.Getenv("DASHBOARD_PORT"); portStr != "" {
if v, err := strconv.Atoi(portStr); err == nil && v > 0 {
dashPort = v
} else {
slog.Warn("invalid DASHBOARD_PORT value, using default 8080", "value", portStr)
}
}

srv := server.New(store, dockerClient, syncTrigger, dashPort)
go srv.Start(ctx)
}

syncInterval := time.Duration(appCfg.SyncIntervalSeconds) * time.Second
ticker := time.NewTicker(syncInterval)
defer ticker.Stop()

doSyncRound := func(cfgs []RepoConfig) {
wg.Add(1)
go func() {
defer wg.Done()
for _, cfg := range cfgs {
if ctx.Err() != nil {
return // shutdown in progress, stop starting new syncs
}
if shouldSkipSync(cfg.Name) {
slog.Info("skipping sync (backoff)", "repo", cfg.Name)
continue
}
syncRepo(ctx, cfg, appCfg, store, dockerClient)
}
refreshContainers(ctx, store, &dockerClient)
}()
wg.Wait() // keep sequential behaviour; WaitGroup lets shutdown detect completion
}

for {
repoCfgs, err := loadRepoConfigs(appCfg)
if err != nil {
slog.Error("failed to get mounted volumes", "err", err)
		os.Exit(1)
}

doSyncRound(repoCfgs)

// Wait for the next tick or a manual sync trigger.
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
// drain any additional queued triggers so we don't double-sync
drainLoop:
for {
select {
case <-syncTrigger:
default:
break drainLoop
}
}
slog.Info("manual sync triggered", "repo", repoName)
resetBackoff(repoName) // manual sync always resets backoff
for _, cfg := range repoCfgs {
if cfg.Name == repoName {
syncRepo(ctx, cfg, appCfg, store, dockerClient)
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
