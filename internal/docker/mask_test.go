package docker

import (
	"strings"
	"testing"
)

func TestMaskEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantMask bool
	}{
		{"plain", "FOO=bar", false},
		{"token", "API_TOKEN=abc123", true},
		{"secret", "MY_SECRET=x", true},
		{"password", "DB_PASSWORD=x", true},
		{"database_url key", "DATABASE_URL=postgres://u:p@host/db", true},
		{"redis url key", "REDIS_URL=redis://host:6379", true},
		{"dsn key", "APP_DSN=host=x", true},
		{"userinfo url value plain key", "CONN=postgres://user:pass@host:5432/db", true},
		{"no userinfo url", "SITE=https://example.com/path", false},
		{"no equals", "NOEQUALS", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := maskEnvVars([]string{tt.in})
			masked := strings.Contains(out[0], "[redacted]")
			if masked != tt.wantMask {
				t.Fatalf("maskEnvVars(%q)=%q, wantMask=%v", tt.in, out[0], tt.wantMask)
			}
		})
	}
}

func TestComposeProjectName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/var/lib/stackd/repos/dockers/My.Stack", "mystack"},
		{"/x/y/Plex_Media", "plex_media"},
		{"/x/y/redis/", "redis"},
		{"UPPER", "upper"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := composeProjectName(tt.in); got != tt.want {
				t.Fatalf("composeProjectName(%q)=%q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
