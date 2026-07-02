package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https", "https://github.com/user/repo.git", false},
		{"http", "http://example.com/repo.git", false},
		{"ssh scheme", "ssh://git@github.com/user/repo.git", false},
		{"git scheme", "git://github.com/user/repo.git", false},
		{"scp-like", "git@github.com:user/repo.git", false},
		{"empty", "", true},
		{"leading dash", "-oProxyCommand=evil", true},
		{"ext transport", "ext::sh -c 'rm -rf /'", true},
		{"file transport", "file::/etc/passwd", true},
		{"fd transport", "fd::0", true},
		{"unsupported scheme", "ftp://example.com/repo", true},
		{"bare word", "not-a-url", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRepoURL(%q) err=%v, wantErr=%v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateRepoName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "myrepo", false},
		{"dashes-dots", "my.repo-1_2", false},
		{"dot", ".", true},
		{"dotdot", "..", true},
		{"traversal", "../etc", true},
		{"slash", "a/b", true},
		{"empty", "", true},
		{"space", "a b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoName(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRepoName(%q) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"token in url", "https://ghp_secret@github.com/x/y.git", "https://***@github.com/x/y.git"},
		{"user pass", "https://user:pass@example.com/repo", "https://***@example.com/repo"},
		{"no creds", "https://github.com/x/y.git", "https://github.com/x/y.git"},
		{"error line", "git clone: https://tok@host/r failed", "git clone: https://***@host/r failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Redact(tt.in); got != tt.want {
				t.Fatalf("Redact(%q)=%q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInjectPAT(t *testing.T) {
	got := injectPAT("https://github.com/user/repo.git", "tok123")
	if !strings.Contains(got, "tok123") || !strings.Contains(got, "@github.com") {
		t.Fatalf("injectPAT did not embed token: %q", got)
	}
	// Invalid URL returned unchanged.
	if got := injectPAT("::::", "tok"); got != "::::" {
		t.Fatalf("injectPAT on invalid URL = %q, want unchanged", got)
	}
}

func TestValidateRef(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "main", false},
		{"origin", "origin", false},
		{"slashes", "feature/x", false},
		{"dots-dashes", "v1.2.3-rc", false},
		{"empty", "", true},
		{"leading dash", "-oProxyCommand=evil", true},
		{"space", "a b", true},
		{"metachar", "a;rm", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRef(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRef(%q) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// TestApplyGitEnvPAT verifies the PAT is passed via the GIT_CONFIG_* environment
// protocol (never on argv) so it cannot leak through ps / procfs.
func TestApplyGitEnvPAT(t *testing.T) {
	cmd := exec.Command("git", "clone", "--", "https://example.com/x", "dest")
	applyGitEnv(cmd, AuthOpts{Type: "pat", PAT: "tok"})

	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "GIT_CONFIG_COUNT=1") {
		t.Fatalf("expected GIT_CONFIG_COUNT=1 in env, got: %v", cmd.Env)
	}
	if !strings.Contains(env, "GIT_CONFIG_KEY_0=http.extraHeader") {
		t.Fatalf("expected GIT_CONFIG_KEY_0=http.extraHeader in env, got: %v", cmd.Env)
	}
	if !strings.Contains(env, "GIT_CONFIG_VALUE_0=Authorization: Basic ") {
		t.Fatalf("expected extraHeader value in env, got: %v", cmd.Env)
	}
	// The raw PAT must not appear verbatim (it is base64-encoded), and nothing
	// PAT-bearing must be present on the command line.
	if strings.Contains(env, "tok:") {
		t.Fatalf("raw PAT leaked into env: %v", cmd.Env)
	}
	for _, a := range cmd.Args {
		if strings.Contains(a, "extraHeader") || strings.Contains(a, "tok") {
			t.Fatalf("credential leaked onto argv: %v", cmd.Args)
		}
	}

	// Non-pat auth must not set the GIT_CONFIG_* vars.
	cmd2 := exec.Command("git", "clone")
	applyGitEnv(cmd2, AuthOpts{Type: "none"})
	if strings.Contains(strings.Join(cmd2.Env, "\n"), "GIT_CONFIG_COUNT") {
		t.Fatalf("GIT_CONFIG_COUNT set for non-pat auth: %v", cmd2.Env)
	}
}

// gitCmd runs a git command in dir with a deterministic identity, failing the
// test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitOutput runs a git command in dir and returns trimmed stdout.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestSyncRepoBareFixture exercises SyncRepo end-to-end against a local bare
// repo, covering both a default ("origin") and non-default ("upstream") remote
// name so the clone/fetch remote-name mismatch (fix #1) stays fixed.
func TestSyncRepoBareFixture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Widen allowed protocols to include the local "file" transport for the
	// duration of this test only; restore afterwards so production stays locked.
	orig := gitAllowProtocol
	gitAllowProtocol = "GIT_ALLOW_PROTOCOL=ssh:https:git:http:file"
	t.Cleanup(func() { gitAllowProtocol = orig })

	for _, remote := range []string{"origin", "upstream"} {
		remote := remote
		t.Run("remote="+remote, func(t *testing.T) {
			root := t.TempDir()
			bare := filepath.Join(root, "bare.git")
			gitCmd(t, root, "init", "--bare", "-b", "main", bare)

			// Seed the bare repo via a scratch work clone.
			seed := filepath.Join(root, "seed")
			gitCmd(t, root, "clone", bare, seed)
			if err := os.WriteFile(filepath.Join(seed, "file.txt"), []byte("hello"), 0644); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, seed, "add", ".")
			gitCmd(t, seed, "commit", "-m", "init")
			gitCmd(t, seed, "push", "origin", "main")
			wantSHA := gitOutput(t, seed, "rev-parse", "HEAD")

			dest := filepath.Join(root, "dest")
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			url := "file://" + bare

			// Fresh clone path.
			if err := SyncRepo(ctx, dest, url, remote, "main", AuthOpts{Type: "none"}); err != nil {
				t.Fatalf("SyncRepo (clone): %v", err)
			}
			if got := gitOutput(t, dest, "remote"); got != remote {
				t.Fatalf("remote name=%q want %q", got, remote)
			}
			gotSHA, err := HeadSHA(ctx, dest)
			if err != nil {
				t.Fatalf("HeadSHA: %v", err)
			}
			if gotSHA != wantSHA {
				t.Fatalf("HEAD=%q want %q", gotSHA, wantSHA)
			}

			// Existing-clone path (fetch + reset) — this is where a non-default
			// remote used to wedge before fix #1.
			if err := SyncRepo(ctx, dest, url, remote, "main", AuthOpts{Type: "none"}); err != nil {
				t.Fatalf("SyncRepo (fetch/reset): %v", err)
			}
		})
	}
}
