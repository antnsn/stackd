package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"stackd/internal/docker"
	"stackd/internal/server"
	"stackd/internal/state"
)

// Function to get mounted volumes
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
// known. Safe to call with a nil dockerClient (no-op).
func refreshContainers(ctx context.Context, store *state.Store, dockerClient *docker.Client) {
	if dockerClient == nil {
		return
	}
	for _, st := range store.GetAllStacks() {
		if st.StackDir == "" {
			continue
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ctrs, err := dockerClient.ListStackContainerDetails(refreshCtx, st.StackDir)
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

func runStacksSync(ctx context.Context, repoDir string, store *state.Store, dockerClient *docker.Client) {
	folderName := strings.ToUpper(filepath.Base(repoDir))
	envVar := "STACKS_DIR_" + folderName
	stacksDir := os.Getenv(envVar)

	if stacksDir == "" {
		return
	}

	entries, err := os.ReadDir(stacksDir)
	if err != nil {
		log.Printf("Failed to read stacks dir %s: %v", stacksDir, err)
		return
	}

	repoName := filepath.Base(repoDir)

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
			log.Printf("Stack %s: no compose.yaml or docker-compose.yml found, skipping", stackName)
			continue
		}

		applyStack(ctx, stackPath, stackName, repoName, store, dockerClient)
	}
}

func runPostSyncCommand(ctx context.Context, repoDir string) {
	folderName := strings.ToUpper(filepath.Base(repoDir))
	envVar := "POST_SYNC_" + folderName
	command := os.Getenv(envVar)

	if command == "" {
		log.Printf("No post-sync command configured for %s (set %s to enable)", repoDir, envVar)
		return
	}

	log.Printf("Running post-sync command for %s: %s", repoDir, command)
	postCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	cmd := exec.CommandContext(postCtx, "sh", "-c", command)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		log.Printf("Post-sync output for %s:\n%s", repoDir, string(output))
	}
	if err != nil {
		log.Printf("Post-sync command failed for %s: %v", repoDir, err)
	} else {
		log.Printf("Post-sync command completed successfully for %s", repoDir)
	}
}

func syncRepo(ctx context.Context, repoDir string, store *state.Store, pullOnly bool, gitUserName, gitUserEmail string, dockerClient *docker.Client) {
	repoName := filepath.Base(repoDir)

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
	}

	// Mark the directory as safe for Git using --system instead of --global
	safeCtx, safeCancel := context.WithTimeout(ctx, 10*time.Second)
	configCmd := exec.CommandContext(safeCtx, "git", "config", "--system", "--add", "safe.directory", "*")
	if err := configCmd.Run(); err != nil {
		log.Printf("Failed to mark directories as safe: %v", err)
	}
	safeCancel()

	fetchCtx, fetchCancel := context.WithTimeout(ctx, 120*time.Second)
	cmd := exec.CommandContext(fetchCtx, "git", "fetch", "origin")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	fetchCancel()
	if err != nil {
		recordError(fmt.Sprintf("Failed to fetch in %s: %v. Output: %s", repoDir, err, string(output)))
		return
	}

	localCtx, localCancel := context.WithTimeout(ctx, 10*time.Second)
	local := exec.CommandContext(localCtx, "git", "rev-parse", "@")
	local.Dir = repoDir
	localSHA, err := local.Output()
	localCancel()
	if err != nil {
		recordError(fmt.Sprintf("Failed to get local SHA in %s: %v", repoDir, err))
		return
	}

	remoteCtx, remoteCancel := context.WithTimeout(ctx, 10*time.Second)
	remote := exec.CommandContext(remoteCtx, "git", "rev-parse", "@{u}")
	remote.Dir = repoDir
	remoteSHA, err := remote.Output()
	remoteCancel()
	if err != nil {
		recordError(fmt.Sprintf("Failed to get remote SHA in %s: %v", repoDir, err))
		return
	}

	currentSHA := strings.TrimSpace(string(localSHA))

	if string(localSHA) != string(remoteSHA) {
		log.Printf("Remote changes detected in %s. Pulling changes...", repoDir)
		pullCtx, pullCancel := context.WithTimeout(ctx, 120*time.Second)
		pullCmd := exec.CommandContext(pullCtx, "git", "pull", "origin", "main")
		pullCmd.Dir = repoDir
		pullOut, pullErr := pullCmd.CombinedOutput()
		pullCancel()
		if pullErr != nil {
			recordError(fmt.Sprintf("Failed to pull in %s: %v. Output: %s", repoDir, pullErr, string(pullOut)))
			return
		}
		// Update SHA after pull
		shaCtx, shaCancel := context.WithTimeout(ctx, 10*time.Second)
		if sha, err := exec.CommandContext(shaCtx, "git", "-C", repoDir, "rev-parse", "HEAD").Output(); err == nil {
			currentSHA = strings.TrimSpace(string(sha))
		}
		shaCancel()
		runPostSyncCommand(ctx, repoDir)
		runStacksSync(ctx, repoDir, store, dockerClient)
	}

	if pullOnly {
		log.Printf("Pull-only mode: skipping add/commit/push for %s", repoDir)
		if store != nil {
			store.UpdateRepo(state.RepoState{
				Name:     repoName,
				LastSync: time.Now(),
				LastSHA:  currentSHA,
				Status:   state.StatusOK,
			})
		}
		return
	}

	// Set git identity before committing
	setGitIdentity(ctx, repoDir, gitUserName, gitUserEmail)

	addCtx, addCancel := context.WithTimeout(ctx, 30*time.Second)
	cmd = exec.CommandContext(addCtx, "git", "add", ".")
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	addCancel()
	if err != nil {
		recordError(fmt.Sprintf("Failed to add changes in %s: %v. Output: %s", repoDir, err, string(output)))
		return
	}

	loc, err := time.LoadLocation(os.Getenv("TZ"))
	if err != nil {
		log.Printf("Failed to load location: %v", err)
		loc = time.UTC
	}
	commitCtx, commitCancel := context.WithTimeout(ctx, 30*time.Second)
	cmd = exec.CommandContext(commitCtx, "git", "commit", "-m", fmt.Sprintf("Automated commit %s", time.Now().In(loc).Format("2006-01-02 15:04:05")))
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	commitCancel()
	if err != nil {
		log.Printf("No changes to commit in %s: %v. Output: %s", repoDir, err, string(output))
		// Not a hard error — nothing to commit is normal.
		if store != nil {
			store.UpdateRepo(state.RepoState{
				Name:     repoName,
				LastSync: time.Now(),
				LastSHA:  currentSHA,
				Status:   state.StatusOK,
			})
		}
		return
	}

	// Refresh SHA after commit
	sha2Ctx, sha2Cancel := context.WithTimeout(ctx, 10*time.Second)
	if sha, err := exec.CommandContext(sha2Ctx, "git", "-C", repoDir, "rev-parse", "HEAD").Output(); err == nil {
		currentSHA = strings.TrimSpace(string(sha))
	}
	sha2Cancel()

	pushCtx, pushCancel := context.WithTimeout(ctx, 120*time.Second)
	cmd = exec.CommandContext(pushCtx, "git", "push", "origin", "main")
	cmd.Dir = repoDir
	output, err = cmd.CombinedOutput()
	pushCancel()
	if err != nil {
		recordError(fmt.Sprintf("Failed to push in %s: %v. Output: %s", repoDir, err, string(output)))
		return
	}

	log.Printf("Successfully synced %s", repoDir)
	if store != nil {
		store.UpdateRepo(state.RepoState{
			Name:     repoName,
			LastSync: time.Now(),
			LastSHA:  currentSHA,
			Status:   state.StatusOK,
		})
	}
}

func main() {
	err := setupSSH()
	if err != nil {
		log.Fatalf("SSH setup failed: %v", err)
	}

	pullOnly := strings.ToLower(os.Getenv("PULL_ONLY")) == "true"
	if pullOnly {
		log.Println("Pull-only mode enabled: skipping git add, commit, and push")
	}

	gitUserName := os.Getenv("GIT_USER_NAME")
	if gitUserName == "" {
		gitUserName = "githubSync"
	}

	gitUserEmail := os.Getenv("GIT_USER_EMAIL")
	if gitUserEmail == "" {
		gitUserEmail = "githubsync@localhost"
	}

	syncIntervalSeconds := 60
	if intervalStr := os.Getenv("SYNC_INTERVAL_SECONDS"); intervalStr != "" {
		if v, err := strconv.Atoi(intervalStr); err == nil && v > 0 {
			syncIntervalSeconds = v
		} else {
			log.Printf("Invalid SYNC_INTERVAL_SECONDS value %q, using default 60", intervalStr)
		}
	}
	log.Printf("Sync interval: %d seconds", syncIntervalSeconds)

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
	dockerClient, err := docker.New()
	if err != nil {
		log.Printf("Warning: Docker client unavailable (%v) — container details and log streaming disabled", err)
		dockerClient = nil
	}

	ctx := context.Background()

	// --- Startup stack scan ---
	// Populate the state store with known stacks at startup so the dashboard
	// shows containers immediately, even when no pull has occurred yet.
	if repoDirs, err := getMountedVolumes(); err == nil {
		for _, repoDir := range repoDirs {
			runStacksSync(ctx, repoDir, store, dockerClient)
		}
		refreshContainers(ctx, store, dockerClient)
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
		go srv.Start()
	}

	syncInterval := time.Duration(syncIntervalSeconds) * time.Second
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		repoDirs, err := getMountedVolumes()
		if err != nil {
			log.Fatalf("Failed to get mounted volumes: %v", err)
		}

		for _, repoDir := range repoDirs {
			syncRepo(ctx, repoDir, store, pullOnly, gitUserName, gitUserEmail, dockerClient)
		}

		// Refresh container state for all known stacks (startup + every tick).
		refreshContainers(ctx, store, dockerClient)

		// Wait for the next tick or a manual sync trigger.
		select {
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
			for _, repoDir := range repoDirs {
				if filepath.Base(repoDir) == repoName {
					syncRepo(ctx, repoDir, store, pullOnly, gitUserName, gitUserEmail, dockerClient)
					break
				}
			}
			refreshContainers(ctx, store, dockerClient)
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
