package docker

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDialProxySpecFromHost(t *testing.T) {
	t.Run("full with custom docker path", func(t *testing.T) {
		got, err := dialProxySpecFromHost(HostSpec{
			ID:             "abc",
			DockerHost:     "ssh://plecto@madara.home:2222",
			KeyPath:        "/keys/host-abc.key",
			KnownHostsFile: "/ssh/known_hosts",
			LocalSocket:    "/ssh/host-abc.sock",
			DockerPath:     "/usr/local/bin/docker",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := dialProxySpec{
			User: "plecto", Host: "madara.home", Port: "2222",
			KeyPath: "/keys/host-abc.key", KnownHostsFile: "/ssh/known_hosts",
			DockerPath: "/usr/local/bin/docker", LocalSocket: "/ssh/host-abc.sock",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dialProxySpecFromHost = %#v\nwant %#v", got, want)
		}
	})

	t.Run("empty docker path defaults", func(t *testing.T) {
		got, err := dialProxySpecFromHost(HostSpec{
			ID:          "abc",
			DockerHost:  "ssh://example.com",
			LocalSocket: "/ssh/host-abc.sock",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.DockerPath != DefaultDockerPath {
			t.Fatalf("DockerPath = %q, want default %q", got.DockerPath, DefaultDockerPath)
		}
	})

	t.Run("missing local socket is an error", func(t *testing.T) {
		if _, err := dialProxySpecFromHost(HostSpec{ID: "abc", DockerHost: "ssh://example.com"}); err == nil {
			t.Fatal("expected error for missing local socket")
		}
	})

	t.Run("bad docker host is an error", func(t *testing.T) {
		if _, err := dialProxySpecFromHost(HostSpec{ID: "abc", DockerHost: "tcp://nope", LocalSocket: "/s.sock"}); err == nil {
			t.Fatal("expected error for non-ssh docker host")
		}
	})
}

// TestDialProxySSHArgs asserts the proxy's per-connection ssh command reuses
// buildSSHArgs: the configured (absolute) dockerPath flows into the remote
// command and dial-stdio is the final token with no trailing junk.
func TestDialProxySSHArgs(t *testing.T) {
	spec, err := dialProxySpecFromHost(HostSpec{
		ID:          "abc",
		DockerHost:  "ssh://plecto@madara.home",
		LocalSocket: "/ssh/host-abc.sock",
		DockerPath:  "/usr/local/bin/docker",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := spec.sshArgs()
	want := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"--", "plecto@madara.home", "/usr/local/bin/docker", "system", "dial-stdio",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sshArgs = %#v\nwant %#v", got, want)
	}
	// dial-stdio must be the final token (nothing trailing that ssh could mangle),
	// and the custom docker path must be present as its own argv token.
	if got[len(got)-1] != "dial-stdio" {
		t.Fatalf("last token = %q, want %q", got[len(got)-1], "dial-stdio")
	}
	foundDockerPath := false
	for _, a := range got {
		if a == "/usr/local/bin/docker" {
			foundDockerPath = true
		}
	}
	if !foundDockerPath {
		t.Fatalf("custom docker path not present in argv: %#v", got)
	}
}

// TestRemoteClientHostUnifiesOnUnix documents that BOTH remote transports resolve
// to the same unix://<localSocket> moby client host: the forward tunnel and the
// dial-stdio proxy each bind the same per-host local socket, so the client (and
// compose) dial the local socket, never ssh:// or tcp://.
func TestRemoteClientHostUnifiesOnUnix(t *testing.T) {
	const sock = "/ssh/host-abc.sock"
	forward, err := forwardTunnelSpec(HostSpec{ID: "abc", DockerHost: "ssh://example.com", LocalSocket: sock})
	if err != nil {
		t.Fatalf("forwardTunnelSpec: %v", err)
	}
	dial, err := dialProxySpecFromHost(HostSpec{ID: "abc", DockerHost: "ssh://example.com", LocalSocket: sock})
	if err != nil {
		t.Fatalf("dialProxySpecFromHost: %v", err)
	}
	fwdHost := forwardClientHost(forward.LocalSocket)
	dialHost := forwardClientHost(dial.LocalSocket)
	want := "unix://" + sock
	if fwdHost != want {
		t.Fatalf("forward client host = %q, want %q", fwdHost, want)
	}
	if dialHost != want {
		t.Fatalf("dial-stdio client host = %q, want %q", dialHost, want)
	}
	if fwdHost != dialHost {
		t.Fatalf("transports disagree on client host: forward=%q dial=%q", fwdHost, dialHost)
	}
}

// TestDialProxyStopSafe verifies Stop is safe/idempotent on a never-started proxy
// and that the manager cleans up a dial-stdio proxy via Stop / StopAll without a
// live ssh process. Mirrors TestTunnelManagerStopSafe.
func TestDialProxyStopSafe(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "host-h.sock")
	p := &dialProxy{hostID: "h", spec: dialProxySpec{LocalSocket: sock}}
	p.Stop() // never started
	p.Stop() // idempotent

	m := NewTunnelManager()
	// Insert a never-started proxy directly (same package) so Stop/StopAll exercise
	// the dial-stdio teardown path without launching ssh.
	m.mu.Lock()
	m.conns["h"] = &dialProxy{hostID: "h", spec: dialProxySpec{LocalSocket: sock}}
	m.mu.Unlock()
	m.Stop("h")
	m.Stop("h") // unknown now: no panic
	m.StopAll() // empty: no panic
}
