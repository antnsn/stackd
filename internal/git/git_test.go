package git

import (
	"strings"
	"testing"
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

func TestPATConfigArgs(t *testing.T) {
	// Non-pat auth => no args.
	if args := patConfigArgs(AuthOpts{Type: "ssh"}); args != nil {
		t.Fatalf("expected nil args for ssh, got %v", args)
	}
	args := patConfigArgs(AuthOpts{Type: "pat", PAT: "tok"})
	if len(args) != 2 || args[0] != "-c" {
		t.Fatalf("unexpected pat args: %v", args)
	}
	if !strings.HasPrefix(args[1], "http.extraHeader=Authorization: Basic ") {
		t.Fatalf("pat header arg malformed: %q", args[1])
	}
	// The raw PAT must not appear verbatim (it is base64-encoded).
	if strings.Contains(args[1], "tok:") {
		t.Fatalf("raw PAT leaked into args: %q", args[1])
	}
}
