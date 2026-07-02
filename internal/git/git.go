package git

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"stackd/internal/execpg"
)

// AuthOpts describes how to authenticate with the remote.
type AuthOpts struct {
	Type       string // "none", "ssh", "pat"
	SSHKeyPath string // path to private key file (for ssh)
	PAT        string // personal access token (for pat — injected via http header)
}

// gitAllowProtocol restricts the transports git is willing to use. It defeats
// malicious remote helpers such as ext::/fd:: that would otherwise let a
// crafted repo URL execute arbitrary commands. It is a var (not a const) solely
// so tests in this package can widen it to include the "file" transport for
// local bare-repo fixtures; production never mutates it.
var gitAllowProtocol = "GIT_ALLOW_PROTOCOL=ssh:https:git:http"

// credentialInURL matches the userinfo portion of a URL (scheme://user:pass@host).
var credentialInURL = regexp.MustCompile(`://[^/@\s]*@`)

// Redact replaces userinfo credentials embedded in URLs (scheme://user:pass@host)
// with scheme://***@host so tokens embedded in a remote URL never reach logs or
// stored error strings.
func Redact(s string) string {
	return credentialInURL.ReplaceAllString(s, "://***@")
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidateRepoName rejects repo names that could be used to escape the clone
// directory via path traversal (e.g. "..", "../x") or that contain path
// separators / shell metacharacters.
func ValidateRepoName(name string) error {
	if name == "." || name == ".." || !repoNameRe.MatchString(name) {
		return fmt.Errorf("invalid repo name %q: must match [A-Za-z0-9._-] and not be '.' or '..'", name)
	}
	return nil
}

var refRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// ValidateRef rejects git remote/branch names that are empty, begin with '-'
// (which git would treat as a flag), or contain characters outside the
// conservative [A-Za-z0-9._/-] set. Used to validate both remote and branch
// before they reach the git command line.
func ValidateRef(name string) error {
	if name == "" {
		return fmt.Errorf("git ref must not be empty")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("git ref %q must not begin with '-'", name)
	}
	if !refRe.MatchString(name) {
		return fmt.Errorf("invalid git ref %q: must match [A-Za-z0-9._/-]", name)
	}
	return nil
}

// scpLikeRe matches scp-style git remotes: user@host:path (no scheme).
var scpLikeRe = regexp.MustCompile(`^[^\s/@:]+@[^\s/:]+:.+$`)

// ValidateRepoURL rejects repo URLs that could be abused for argument injection
// or arbitrary command execution. It allows https/http/ssh/git schemes and
// scp-like git@host:path syntax, and rejects the ext::/file::/fd:: transports
// as well as any value beginning with '-' (which git would treat as a flag).
func ValidateRepoURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty repo URL")
	}
	if strings.HasPrefix(raw, "-") {
		return fmt.Errorf("repo URL must not begin with '-'")
	}
	lower := strings.ToLower(raw)
	for _, bad := range []string{"ext::", "file::", "fd::"} {
		if strings.HasPrefix(lower, bad) {
			return fmt.Errorf("repo URL uses forbidden transport %q", bad)
		}
	}
	// scp-like syntax (git@host:path) has no scheme.
	if !strings.Contains(raw, "://") {
		if scpLikeRe.MatchString(raw) {
			// Reject a host component beginning with '-' (e.g. user@-oProxy...:x)
			// which ssh would otherwise parse as an option.
			at := strings.Index(raw, "@")
			rest := raw[at+1:]
			host := rest[:strings.Index(rest, ":")]
			if strings.HasPrefix(host, "-") {
				return fmt.Errorf("repo URL host must not begin with '-'")
			}
			return nil
		}
		return fmt.Errorf("repo URL %q is not a valid git remote", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse repo URL: %w", err)
	}
	if strings.HasPrefix(u.Host, "-") {
		return fmt.Errorf("repo URL host must not begin with '-'")
	}
	switch u.Scheme {
	case "https", "http", "ssh", "git":
		return nil
	default:
		return fmt.Errorf("unsupported repo URL scheme %q", u.Scheme)
	}
}

// SyncRepo brings destDir to the tip of remote/branch. When destDir has no .git
// it performs a fresh clone; otherwise it fetches and hard-resets so that a
// force-pushed remote or a locally-dirty working tree cannot wedge the repo
// into permanent failure (the historical "git pull" fragility). Credentials
// are supplied per-command (SSH key env or an http.extraHeader) and are never
// written to .git/config.
func SyncRepo(ctx context.Context, destDir, repoURL, remote, branch string, opts AuthOpts) error {
	if remote == "" {
		remote = "origin"
	}
	gitDir := filepath.Join(destDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if err := cloneRepo(ctx, repoURL, destDir, remote, opts); err != nil {
			return err
		}
	}
	// Always fetch + hard-reset, even right after a fresh clone, so the configured
	// branch is honoured immediately and a dirty/force-pushed tree self-heals.
	return fetchReset(ctx, destDir, remote, branch, opts)
}

func cloneRepo(ctx context.Context, repoURL, destDir, remote string, opts AuthOpts) error {
	// Name the remote to match the configured remote so subsequent fetch/reset
	// against that remote succeed (a non-"origin" remote otherwise wedges the repo).
	// "--" ensures a repoURL or destDir starting with '-' cannot be parsed as a flag.
	args := []string{"clone", "--origin", remote, "--", repoURL, destDir}
	cmd := execpg.CommandContext(ctx, "git", args...)
	applyGitEnv(cmd, opts)
	slog.Debug("git clone", "dest", destDir, "remote", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w\noutput: %s", err, Redact(string(out)))
	}
	return nil
}

func fetchReset(ctx context.Context, repoDir, remote, branch string, opts AuthOpts) error {
	fetchArgs := []string{"-C", repoDir, "fetch", "--prune", "--", remote}
	fetchCmd := execpg.CommandContext(ctx, "git", fetchArgs...)
	applyGitEnv(fetchCmd, opts)
	slog.Debug("git fetch", "dir", repoDir, "remote", remote)
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %w\noutput: %s", err, Redact(string(out)))
	}

	ref := "FETCH_HEAD"
	if branch != "" && branch != "HEAD" {
		ref = remote + "/" + branch
	}
	// --end-of-options terminates flag parsing so a ref beginning with '-' can
	// never be interpreted as an option (the literal "--" is rejected by
	// "reset --hard" as a pathspec separator, so --end-of-options is used).
	resetCmd := execpg.CommandContext(ctx, "git", "-C", repoDir, "reset", "--hard", "--end-of-options", ref)
	applyGitEnv(resetCmd, opts)
	slog.Debug("git reset --hard", "dir", repoDir, "ref", ref)
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard %s: %w\noutput: %s", ref, err, Redact(string(out)))
	}
	return nil
}

// HeadSHA returns the current HEAD commit SHA for the repo at repoDir.
func HeadSHA(ctx context.Context, repoDir string) (string, error) {
	cmd := execpg.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	slog.Debug("git rev-parse HEAD", "dir", repoDir)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git rev-parse HEAD %s: %w: %s", repoDir, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git rev-parse HEAD %s: %w", repoDir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// injectPAT returns a copy of rawURL with the PAT injected as username (and empty
// password). Retained for reference/testing; the clone/fetch paths inject the
// credential via an http.extraHeader instead so it never lands in .git/config.
func injectPAT(rawURL, pat string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.UserPassword(pat, "")
	return u.String()
}

// applyGitEnv sets GIT_ALLOW_PROTOCOL on every git command, GIT_SSH_COMMAND when
// SSH auth is configured, and — for PAT auth — the http.extraHeader credential
// via git's GIT_CONFIG_COUNT/KEY/VALUE protocol. Passing the PAT through the
// environment (not "-c" on argv) keeps the token out of the process command line,
// which is readable by any local user via ps / /proc/<pid>/cmdline.
func applyGitEnv(cmd *exec.Cmd, opts AuthOpts) {
	env := append(os.Environ(), gitAllowProtocol)
	if opts.Type == "ssh" && opts.SSHKeyPath != "" {
		env = append(env, sshCommandEnv(opts.SSHKeyPath))
	}
	if opts.Type == "pat" && opts.PAT != "" {
		basic := base64.StdEncoding.EncodeToString([]byte(opts.PAT + ":"))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	cmd.Env = env
}

// sshCommandEnv builds the GIT_SSH_COMMAND value.
//
// StrictHostKeyChecking=accept-new implements trust-on-first-use: the first key
// seen for a host is pinned into a managed known_hosts file, and any later
// change to that host's key is rejected (defends against MITM after the initial
// fetch). This replaces the previous StrictHostKeyChecking=no +
// UserKnownHostsFile=/dev/null, which accepted any key on every connection.
//
// Timeout flags are critical: without ConnectTimeout / ServerAliveInterval the
// ssh subprocess can hang indefinitely on a dead TCP connection, and because
// ssh is spawned by git it survived the historical SIGKILL-the-direct-child
// cancellation, leaking PIDs until the per-user nproc limit was hit. BatchMode
// disables interactive prompts so a missing key fails fast.
func sshCommandEnv(keyPath string) string {
	kh := knownHostsFile()
	return fmt.Sprintf(
		"GIT_SSH_COMMAND=ssh -i %s"+
			" -o StrictHostKeyChecking=accept-new"+
			" -o UserKnownHostsFile=%s"+
			" -o BatchMode=yes"+
			" -o ConnectTimeout=10"+
			" -o ServerAliveInterval=10"+
			" -o ServerAliveCountMax=3",
		keyPath, kh,
	)
}

// knownHostsFile returns the path to the managed known_hosts file, creating its
// parent directory (0700) if needed. Override the directory with SSH_KNOWN_HOSTS_DIR.
func knownHostsFile() string {
	dir := os.Getenv("SSH_KNOWN_HOSTS_DIR")
	if dir == "" {
		// Fall back to a private dir only if the operator/main did not set one.
		// main.go points this at <cloneDir>/.ssh; this /tmp path is a last resort.
		dir = "/tmp/stackd-ssh"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		slog.Warn("could not create ssh known_hosts dir", "dir", dir, "err", err)
	}
	return filepath.Join(dir, "known_hosts")
}
