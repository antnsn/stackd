package hostres

import (
	"path/filepath"
	"strings"
	"testing"

	"stackd/internal/docker"
)

func TestHostInfoTransportDefault(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", docker.TransportForward},
		{"forward", docker.TransportForward},
		{"dial-stdio", docker.TransportDialStdio},
	}
	for _, tt := range tests {
		if got := (HostInfo{Transport: tt.in}).transport(); got != tt.want {
			t.Fatalf("transport(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLocalSocketPath(t *testing.T) {
	r := New(nil, nil, nil, "/clone/.ssh", "/clone/.ssh/known_hosts")
	got := r.localSocketPath("abc-123")
	want := filepath.Join("/clone/.ssh", "host-abc-123.sock")
	if got != want {
		t.Fatalf("localSocketPath = %q, want %q", got, want)
	}
}

// TestForwardComposeDockerHost documents the DOCKER_HOST that a remote host's
// ComposeEnv hands to `docker compose`: a unix:// URL for the local socket
// exposed by the host's ssh transport, with no HOME/.ssh/config involved. This
// holds for BOTH transports — the forward tunnel and the dial-stdio proxy both
// bind the same per-host local socket — so compose never invokes ssh or a remote
// docker binary itself. This asserts the pure mapping so the selection is covered
// without launching a live tunnel/proxy.
func TestForwardComposeDockerHost(t *testing.T) {
	r := New(nil, nil, nil, "/clone/.ssh", "/clone/.ssh/known_hosts")
	sock := r.localSocketPath("h1")
	got := docker.ForwardComposeDockerHost(sock)
	want := "unix://" + filepath.Join("/clone/.ssh", "host-h1.sock")
	if got != want {
		t.Fatalf("remote DOCKER_HOST = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "ssh://") {
		t.Fatalf("remote DOCKER_HOST must not be ssh://, got %q", got)
	}
}
