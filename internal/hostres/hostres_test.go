package hostres

import (
	"strings"
	"testing"
)

func TestBuildSSHConfig(t *testing.T) {
	cfg := BuildSSHConfig("example.com", "deploy", "2222", "/keys/host-abc.key", "/ssh/known_hosts")

	wantLines := []string{
		"Host example.com",
		"    User deploy",
		"    Port 2222",
		"    IdentityFile /keys/host-abc.key",
		"    IdentitiesOnly yes",
		"    UserKnownHostsFile /ssh/known_hosts",
		"    StrictHostKeyChecking accept-new",
		"    BatchMode yes",
		"    ConnectTimeout 10",
	}
	for _, want := range wantLines {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing line %q\ngot:\n%s", want, cfg)
		}
	}
}

func TestBuildSSHConfigOmitsEmptyOptionals(t *testing.T) {
	cfg := BuildSSHConfig("host", "", "", "", "")
	if strings.Contains(cfg, "User ") {
		t.Errorf("expected no User line, got:\n%s", cfg)
	}
	if strings.Contains(cfg, "Port ") {
		t.Errorf("expected no Port line, got:\n%s", cfg)
	}
	if strings.Contains(cfg, "IdentityFile") {
		t.Errorf("expected no IdentityFile line, got:\n%s", cfg)
	}
	if strings.Contains(cfg, "UserKnownHostsFile") {
		t.Errorf("expected no UserKnownHostsFile line, got:\n%s", cfg)
	}
	// The fixed hardening lines must always be present.
	if !strings.Contains(cfg, "StrictHostKeyChecking accept-new") {
		t.Errorf("expected StrictHostKeyChecking line, got:\n%s", cfg)
	}
}
