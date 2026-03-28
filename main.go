package main

import (
"context"
"fmt"
"log"
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
log.Printf("Repo %q suspended after %d consecutive failures; trigger manual sync to resume", repoName, b.failures)
return true
}
multiplier := time.Duration(1 << b.failures) // 2, 4, 8, 16...
backoff := multiplier * baseInterval
maxBackoff := 8 * baseInterval
if backoff > maxBackoff {
backoff = maxBackoff
}
b.nextAllowed = time.Now().Add(backoff)
log.Printf("Repo %q sync backoff: next attempt in %s (failure %d/%d)", repoName, backoff, b.failures, maxSyncFailures)
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
log.Printf("SSH config written to %s", configPath)

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
log.Printf("Failed to set git user.name in %s: %v", repoDir, err)
}

emailCtx, emailCancel := context.WithTimeout(ctx, 10*time.Second)
defer emailCancel()
emailCmd := exec.CommandContext(emailCtx, "git", "config", "user.email", userEmail)
emailCmd.Dir = repoDir
if err := emailCmd.Run(); err != nil {
log.Printf("Failed to set git user.email in %s: %v", repoDir, err)
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
log.Printf("Applying stack %s (Infisical disabled)", stackName)
return exec.CommandContext(ctx, "docker", "compose", "up", "-d")
}

args := []string{"run"}
configured := false

tomlPath := filepath.Join(stackPath, "infisical.toml")
if _, err := os.Stat(tomlPath); err == nil {
args = append(args, "--config="+tomlPath)
log.Printf("Stack %s: using per-stack infisical.toml", stackName)
configured = true
} else if token := os.Getenv("INFISICAL_TOKEN"); token != "" {
args = append(args, "--token="+token)
infisicalEnv := os.Getenv("INFISICAL_ENV")
if infisicalEnv == "" {
infisicalEnv = "prod"
}
args = append(args, "--env="+infisicalEnv)
log.Printf("Stack %s: using global INFISICAL_TOKEN (env: %s)", stackName, infisicalEnv)
configured = true
} else {
log.Printf("Warning: INFISICAL_ENABLED=true but no infisical.toml or INFISICAL_TOKEN found for stack %s, applying without secrets injection", stackName)
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
log.Printf("Docker client reconnected successfully")
*dockerClientPtr = c
} else {
log.Printf("Docker reconnection failed: %v", err)
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
log.Printf("refreshContainers %s/%s: %v", st.RepoName, st.Name, err)
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
})
}
store.UpdateStackContainers(st.RepoName, st.Name, containers)
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
log.Printf("Stack %s output:\n%s", stackName, outputStr)
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
log.Printf("Stack %s: container lookup failed: %v", stackName, dErr)
} else {
for _, dc := range ctrs {
st.Containers = append(st.Containers, state.ContainerDetail{
ID:        dc.ID,
Name:      dc.Name,
Image:     dc.Image,
Status:    dc.Status,
StartedAt: dc.StartedAt,
})
}
}
}
}
store.UpdateStack(st)
}

if err != nil {
log.Printf("Stack %s failed: %v", stackName, err)
} else {
log.Printf("Stack %s applied successfully", stackName)
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
log.Printf("Warning: failed to parse %s: %v", configPath, err)
return result
}

log.Printf("Loaded config from %s (%d repos)", configPath, len(cfg.Repos))
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
log.Printf("Failed to read stacks dir %s: %v", cfg.StacksDir, err)
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
log.Printf("Stack %s: no compose.yaml or docker-compose.yml found, skipping", stackName)
continue
}

applyStack(ctx, stackPath, stackName, cfg.Name, store, dockerClient)
}
}

func runPostSyncCommand(ctx context.Context, cfg RepoConfig) {
if cfg.PostSyncCmd == "" {
return
}

log.Printf("Running post-sync command for %s: %s", cfg.Name, cfg.PostSyncCmd)
postCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
defer cancel()
cmd := exec.CommandContext(postCtx, "sh", "-c", cfg.PostSyncCmd)
cmd.Dir = cfg.Dir
output, err := cmd.CombinedOutput()
if len(output) > 0 {
log.Printf("Post-sync output for %s:\n%s", cfg.Name, string(output))
}
if err != nil {
log.Printf("Post-sync command failed for %s: %v", cfg.Name, err)
} else {
log.Printf("Post-sync command completed successfully for %s", cfg.Name)
}
}

func syncRepo(ctx context.Context, cfg RepoConfig, appCfg AppConfig, store *state.Store, dockerClient *docker.Client) {
repoName := cfg.Name
pullOnly := cfg.PullOnly || appCfg.PullOnly

if store != nil {
existing, ok := store.GetRepo(repoName)
if !ok {
existing = state.RepoState{Name: repoName}
}
existing.Status = state.StatusSyncing
store.UpdateRepo(existing)
}

recordError := func(msg string) {
log.Print(msg)
if store != nil {
existing, _ := store.GetRepo(repoName)
existing.Name = repoName
existing.Status = state.StatusError
existing.LastError = msg
store.UpdateRepo(existing)
}
recordSyncFailure(repoName, appCfg.SyncInterval)
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
log.Printf("Failed to mark directories as safe: %v", err)
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
log.Printf("Remote changes detected in %s. Pulling changes...", cfg.Dir)
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
log.Printf("Pull-only mode: skipping add/commit/push for %s", cfg.Dir)
if store != nil {
store.UpdateRepo(state.RepoState{
Name:     repoName,
LastSync: time.Now(),
LastSHA:  currentSHA,
Status:   state.StatusOK,
})
}
recordSyncSuccess(repoName)
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
log.Printf("Failed to load location: %v", err)
loc = time.UTC
}
commitCtx, commitCancel := context.WithTimeout(ctx, 30*time.Second)
cmd = exec.CommandContext(commitCtx, "git", "commit", "-m", fmt.Sprintf("Automated commit %s", time.Now().In(loc).Format("2006-01-02 15:04:05")))
cmd.Dir = cfg.Dir
output, err = cmd.CombinedOutput()
commitCancel()
if err != nil {
log.Printf("No changes to commit in %s: %v. Output: %s", cfg.Dir, err, string(output))
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

log.Printf("Successfully synced %s", cfg.Dir)
if store != nil {
store.UpdateRepo(state.RepoState{
Name:     repoName,
LastSync: time.Now(),
LastSHA:  currentSHA,
Status:   state.StatusOK,
})
}
recordSyncSuccess(repoName)
}

func main() {
err := setupSSH()
if err != nil {
log.Fatalf("SSH setup failed: %v", err)
}

appCfg := loadAppConfig()

if appCfg.PullOnly {
log.Println("Pull-only mode enabled: skipping git add, commit, and push")
}

log.Printf("Sync interval: %d seconds", appCfg.SyncIntervalSeconds)

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
log.Printf("Warning: Docker client unavailable (%v) — container details and log streaming disabled", err)
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
log.Printf("Invalid DASHBOARD_PORT value %q, using default 8080", portStr)
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
log.Printf("Skipping sync for %q (backoff)", cfg.Name)
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
log.Fatalf("Failed to get mounted volumes: %v", err)
}

doSyncRound(repoCfgs)

// Wait for the next tick or a manual sync trigger.
select {
case <-ctx.Done():
log.Printf("Shutdown signal received, waiting for in-flight operations to complete...")
ticker.Stop()
waitDone := make(chan struct{})
go func() { wg.Wait(); close(waitDone) }()
select {
case <-waitDone:
log.Printf("All operations completed, shutting down cleanly")
case <-time.After(30 * time.Second):
log.Printf("Shutdown timeout reached, forcing exit")
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
log.Printf("Manual sync triggered for %q", repoName)
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
