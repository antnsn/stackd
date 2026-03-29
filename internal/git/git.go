package git

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AuthOpts describes how to authenticate with the remote.
type AuthOpts struct {
	Type       string // "none", "ssh", "pat"
	SSHKeyPath string // path to private key file (for ssh)
	PAT        string // personal access token (for pat — injected into URL)
}

// Clone clones repoURL into destDir. If destDir already contains a git repo
// (.git exists), Pull is called instead.
func Clone(ctx context.Context, repoURL, destDir string, opts AuthOpts) error {
	gitDir := filepath.Join(destDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Repo already exists — pull instead.
		return Pull(ctx, destDir, "origin", "HEAD", opts)
	}

	cloneURL := repoURL
	if opts.Type == "pat" && opts.PAT != "" {
		cloneURL = injectPAT(repoURL, opts.PAT)
	}

	args := []string{"clone", cloneURL, destDir}
	cmd := exec.CommandContext(ctx, "git", args...)
	applySSHEnv(cmd, opts)
	slog.Debug("git clone", "url", repoURL, "dest", destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %s: %w\noutput: %s", repoURL, err, out)
	}
	return nil
}

// Pull fetches and merges the remote branch into current HEAD.
func Pull(ctx context.Context, repoDir, remote, branch string, opts AuthOpts) error {
	if opts.Type == "pat" && opts.PAT != "" {
		// For PAT auth we need to set the remote URL with credentials embedded.
		// Get current remote URL first.
		getURLCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "get-url", remote)
		rawURL, err := getURLCmd.Output()
		if err == nil {
			patURL := injectPAT(strings.TrimSpace(string(rawURL)), opts.PAT)
			setURLCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "set-url", remote, patURL)
			if out, err := setURLCmd.CombinedOutput(); err != nil {
				slog.Warn("git remote set-url failed", "err", err, "output", string(out))
			} else {
				// Restore original URL after pull.
				origURL := strings.TrimSpace(string(rawURL))
				defer func() {
					restoreCmd := exec.CommandContext(context.Background(), "git", "-C", repoDir, "remote", "set-url", remote, origURL)
					if out, err := restoreCmd.CombinedOutput(); err != nil {
						slog.Warn("git remote restore-url failed", "err", err, "output", string(out))
					}
				}()
			}
		}
	}

	args := []string{"-C", repoDir, "pull", remote}
	if branch != "" && branch != "HEAD" {
		args = append(args, branch)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	applySSHEnv(cmd, opts)
	slog.Debug("git pull", "dir", repoDir, "remote", remote, "branch", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git pull %s %s: %w\noutput: %s", remote, branch, err, out)
	}
	return nil
}

// HeadSHA returns the current HEAD commit SHA for the repo at repoDir.
func HeadSHA(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	slog.Debug("git rev-parse HEAD", "dir", repoDir)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD %s: %w", repoDir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// injectPAT returns a copy of rawURL with the PAT injected as username (and empty password).
func injectPAT(rawURL, pat string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.UserPassword(pat, "")
	return u.String()
}

// applySSHEnv sets GIT_SSH_COMMAND on cmd if SSH auth is configured.
func applySSHEnv(cmd *exec.Cmd, opts AuthOpts) {
	if opts.Type != "ssh" || opts.SSHKeyPath == "" {
		return
	}
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", opts.SSHKeyPath),
	)
}
