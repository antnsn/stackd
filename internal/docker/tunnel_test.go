package docker

import (
	"reflect"
	"testing"
)

func TestSSHForwardArgs(t *testing.T) {
	tests := []struct {
		name string
		spec tunnelSpec
		want []string
	}{
		{
			name: "full remote with user port key knownhosts",
			spec: tunnelSpec{
				User: "deploy", Host: "example.com", Port: "2222",
				KeyPath: "/keys/host-abc.key", KnownHostsFile: "/ssh/known_hosts",
				LocalSocket: "/ssh/host-abc.sock", RemoteSocket: "/var/run/docker.sock",
			},
			want: []string{
				"-N", "-T",
				"-i", "/keys/host-abc.key",
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "UserKnownHostsFile=/ssh/known_hosts",
				"-o", "BatchMode=yes",
				"-o", "ExitOnForwardFailure=yes",
				"-o", "ConnectTimeout=10",
				"-o", "ServerAliveInterval=10",
				"-o", "ServerAliveCountMax=3",
				"-p", "2222",
				"-L", "/ssh/host-abc.sock:/var/run/docker.sock",
				"deploy@example.com",
			},
		},
		{
			name: "no user no port no key",
			spec: tunnelSpec{
				Host: "example.com", KnownHostsFile: "/ssh/known_hosts",
				LocalSocket: "/ssh/host-x.sock", RemoteSocket: "/run/docker.sock",
			},
			want: []string{
				"-N", "-T",
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "UserKnownHostsFile=/ssh/known_hosts",
				"-o", "BatchMode=yes",
				"-o", "ExitOnForwardFailure=yes",
				"-o", "ConnectTimeout=10",
				"-o", "ServerAliveInterval=10",
				"-o", "ServerAliveCountMax=3",
				"-L", "/ssh/host-x.sock:/run/docker.sock",
				"example.com",
			},
		},
		{
			name: "no knownhosts omits option",
			spec: tunnelSpec{
				User: "root", Host: "10.0.0.5",
				LocalSocket: "/s.sock", RemoteSocket: "/var/run/docker.sock",
			},
			want: []string{
				"-N", "-T",
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "BatchMode=yes",
				"-o", "ExitOnForwardFailure=yes",
				"-o", "ConnectTimeout=10",
				"-o", "ServerAliveInterval=10",
				"-o", "ServerAliveCountMax=3",
				"-L", "/s.sock:/var/run/docker.sock",
				"root@10.0.0.5",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sshForwardArgs(tt.spec)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("sshForwardArgs = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

// TestSSHForwardArgsDestinationLast asserts the destination is the final argv
// element with no trailing remote command (which ssh would otherwise execute).
func TestSSHForwardArgsDestinationLast(t *testing.T) {
	args := sshForwardArgs(tunnelSpec{
		User: "deploy", Host: "example.com",
		LocalSocket: "/s.sock", RemoteSocket: "/var/run/docker.sock",
	})
	if got := args[len(args)-1]; got != "deploy@example.com" {
		t.Fatalf("destination is not last: last=%q args=%#v", got, args)
	}
	// -N (no remote command) and -T (no tty) must be present.
	if args[0] != "-N" || args[1] != "-T" {
		t.Fatalf("expected leading -N -T, got %#v", args[:2])
	}
}

func TestForwardTunnelSpec(t *testing.T) {
	t.Run("full with explicit remote socket", func(t *testing.T) {
		got, err := forwardTunnelSpec(HostSpec{
			ID:             "abc",
			DockerHost:     "ssh://deploy@example.com:2222",
			KeyPath:        "/keys/host-abc.key",
			KnownHostsFile: "/ssh/known_hosts",
			RemoteSocket:   "/run/user/1000/docker.sock",
			LocalSocket:    "/ssh/host-abc.sock",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := tunnelSpec{
			User: "deploy", Host: "example.com", Port: "2222",
			KeyPath: "/keys/host-abc.key", KnownHostsFile: "/ssh/known_hosts",
			LocalSocket: "/ssh/host-abc.sock", RemoteSocket: "/run/user/1000/docker.sock",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("forwardTunnelSpec = %#v\nwant %#v", got, want)
		}
	})

	t.Run("empty remote socket defaults", func(t *testing.T) {
		got, err := forwardTunnelSpec(HostSpec{
			ID:          "abc",
			DockerHost:  "ssh://example.com",
			LocalSocket: "/ssh/host-abc.sock",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.RemoteSocket != DefaultRemoteSocket {
			t.Fatalf("RemoteSocket = %q, want default %q", got.RemoteSocket, DefaultRemoteSocket)
		}
	})

	t.Run("missing local socket is an error", func(t *testing.T) {
		if _, err := forwardTunnelSpec(HostSpec{ID: "abc", DockerHost: "ssh://example.com"}); err == nil {
			t.Fatal("expected error for missing local socket")
		}
	})

	t.Run("bad docker host is an error", func(t *testing.T) {
		if _, err := forwardTunnelSpec(HostSpec{ID: "abc", DockerHost: "tcp://nope", LocalSocket: "/s.sock"}); err == nil {
			t.Fatal("expected error for non-ssh docker host")
		}
	})
}

func TestForwardClientHost(t *testing.T) {
	if got := forwardClientHost("/ssh/host-abc.sock"); got != "unix:///ssh/host-abc.sock" {
		t.Fatalf("forwardClientHost = %q", got)
	}
	if got := ForwardComposeDockerHost("/ssh/host-abc.sock"); got != "unix:///ssh/host-abc.sock" {
		t.Fatalf("ForwardComposeDockerHost = %q", got)
	}
}

// TestTunnelManagerStopSafe verifies Stop / StopAll on an empty manager (and for
// an unknown host id) do not panic — no live tunnel is started.
func TestTunnelManagerStopSafe(t *testing.T) {
	m := NewTunnelManager()
	m.Stop("does-not-exist")
	m.StopAll()
}
