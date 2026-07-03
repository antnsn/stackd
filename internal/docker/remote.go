package docker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// HostSpec describes how to reach a Docker daemon. An empty DockerHost selects
// the local socket (see Registry.Get); otherwise DockerHost is
// "ssh://user@host[:port]" and the connection is tunnelled over `ssh ... docker
// system dial-stdio`.
type HostSpec struct {
	ID             string // host id used as the registry cache key (e.g. "local" or a uuid)
	DockerHost     string // "" = local socket; else ssh://user@host[:port]
	KeyPath        string // path to the decrypted private key file (remote only)
	KnownHostsFile string // managed known_hosts path (remote only)
}

// ValidateDockerHost accepts an empty string (local socket) or an
// ssh://user@host[:port] URL. Every other transport (tcp://, http://, unix://,
// junk) is rejected in Phase 1. A host beginning with '-' is rejected so it can
// never be parsed by ssh as an option.
func ValidateDockerHost(s string) error {
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "ssh://") {
		return fmt.Errorf("docker host must be empty (local) or ssh://user@host[:port], got %q", s)
	}
	// Reject any path/query component: ssh://host/foo is not a valid daemon URL.
	if strings.ContainsAny(strings.TrimPrefix(s, "ssh://"), "/?#") {
		return fmt.Errorf("docker host %q must not contain a path", s)
	}
	if _, _, _, err := parseSSHHost(s); err != nil {
		return err
	}
	return nil
}

// ParseSSHHost is the exported form of parseSSHHost, used by callers that build
// a `docker compose` environment for a remote host.
func ParseSSHHost(dockerHost string) (user, host, port string, err error) {
	return parseSSHHost(dockerHost)
}

// parseSSHHost extracts the user, host and port from an ssh://user@host[:port]
// docker host value. It is pure so it can be unit-tested. host and user must not
// begin with '-' (which ssh would treat as an option). user and port may be
// empty (ssh then uses its own defaults).
func parseSSHHost(dockerHost string) (user, host, port string, err error) {
	if !strings.HasPrefix(dockerHost, "ssh://") {
		return "", "", "", fmt.Errorf("docker host %q is not an ssh:// URL", dockerHost)
	}
	u, perr := url.Parse(dockerHost)
	if perr != nil {
		return "", "", "", fmt.Errorf("parse docker host %q: %w", dockerHost, perr)
	}
	host = u.Hostname()
	user = u.User.Username()
	port = u.Port()
	if host == "" {
		return "", "", "", fmt.Errorf("docker host %q has no host component", dockerHost)
	}
	// url.Parse decodes percent-escapes, so a value like ssh://u%0aProxyCommand...@h
	// would smuggle a newline into the user component and, via the generated
	// ~/.ssh/config, inject an ssh directive (e.g. ProxyCommand) that runs during
	// the local `docker compose` ssh call. Reject any control/space character in
	// every component.
	for _, part := range []string{user, host, port} {
		if strings.IndexFunc(part, func(r rune) bool { return r < 0x20 || r == 0x7f || r == ' ' }) >= 0 {
			return "", "", "", fmt.Errorf("docker host %q contains an invalid character", dockerHost)
		}
	}
	if strings.HasPrefix(host, "-") {
		return "", "", "", fmt.Errorf("docker host %q: host must not begin with '-'", dockerHost)
	}
	if strings.HasPrefix(user, "-") {
		return "", "", "", fmt.Errorf("docker host %q: user must not begin with '-'", dockerHost)
	}
	if strings.HasPrefix(port, "-") {
		return "", "", "", fmt.Errorf("docker host %q: port must not begin with '-'", dockerHost)
	}
	return user, host, port, nil
}

// buildSSHArgs assembles the argv (after "ssh") for tunnelling the Docker API
// over `docker system dial-stdio`. It mirrors what docker/cli's connhelper does.
// It is pure so the argv can be asserted in tests. keyPath and knownHosts may be
// empty (the corresponding option is then omitted).
func buildSSHArgs(user, host, port, keyPath, knownHosts string) []string {
	var args []string
	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}
	args = append(args,
		"-o", "StrictHostKeyChecking=accept-new",
	)
	if knownHosts != "" {
		args = append(args, "-o", "UserKnownHostsFile="+knownHosts)
	}
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
	)
	if port != "" {
		args = append(args, "-p", port)
	}
	dest := host
	if user != "" {
		dest = user + "@" + host
	}
	// In OpenSSH everything after the destination is the remote command, so a
	// local option terminator ("--") must come BEFORE the destination, not after
	// (after it, ssh would run "-- docker …" on the remote and fail). The remote
	// command tokens below are fixed constants; user/host/port are validated to
	// contain no leading '-' or control characters.
	args = append(args, "--", dest, "docker", "system", "dial-stdio")
	return args
}

// commandConn adapts the stdin/stdout of a long-lived `ssh ... dial-stdio`
// process to a net.Conn. Closing the conn kills the ssh process, so a client
// closing an idle pooled connection (or Registry eviction) tears down the
// tunnel and never leaks the subprocess. This reimplements the minimal subset
// of docker/cli's connhelper commandconn.
type commandConn struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	closeOnce  sync.Once
	closeErr   error
	localAddr  net.Addr
	remoteAddr net.Addr
}

func (c *commandConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *commandConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

func (c *commandConn) Close() error {
	c.closeOnce.Do(func() {
		// Closing stdin lets the remote side see EOF; then kill and reap so no
		// zombie/leaked PID remains.
		_ = c.stdin.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		_ = c.stdout.Close()
		c.closeErr = c.cmd.Wait()
	})
	return c.closeErr
}

func (c *commandConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *commandConn) RemoteAddr() net.Addr { return c.remoteAddr }

// Deadlines are not enforceable over a pipe-backed subprocess. The moby client
// relies on context cancellation (which closes the connection) for timeouts, so
// these are safe no-ops.
func (c *commandConn) SetDeadline(t time.Time) error      { return nil }
func (c *commandConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *commandConn) SetWriteDeadline(t time.Time) error { return nil }

type addr struct{ net, s string }

func (a addr) Network() string { return a.net }
func (a addr) String() string  { return a.s }

// newSSHDialer returns a DialContext function that spawns one ssh process per
// dial. Each dial is an independent tunnel; a transient remote outage therefore
// self-heals on the next dial without any explicit cache eviction.
func newSSHDialer(spec HostSpec) (func(ctx context.Context, network, address string) (net.Conn, error), error) {
	user, host, port, err := parseSSHHost(spec.DockerHost)
	if err != nil {
		return nil, err
	}
	args := buildSSHArgs(user, host, port, spec.KeyPath, spec.KnownHostsFile)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		// The process is intentionally NOT bound to the dial ctx: net/http reuses
		// pooled connections across requests, so tying the ssh lifetime to a single
		// request's context would kill a tunnel still needed by later requests.
		// Lifetime is instead bounded by commandConn.Close (called by the client on
		// eviction / idle-close). ConnectTimeout=10 bounds a hanging connect.
		cmd := exec.Command("ssh", args...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("ssh stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("ssh stdout pipe: %w", err)
		}
		// Surface ssh diagnostics (auth failures, host-key issues) on stackd's
		// stderr so remote connection problems are debuggable.
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start ssh to %s: %w", spec.DockerHost, err)
		}
		return &commandConn{
			cmd:        cmd,
			stdin:      stdin,
			stdout:     stdout,
			localAddr:  addr{network, "ssh-dial-stdio"},
			remoteAddr: addr{network, spec.DockerHost},
		}, nil
	}, nil
}

// NewRemote builds a Docker client that reaches the daemon at spec.DockerHost
// over an ssh dial-stdio tunnel. The tcp://placeholder host is a required dummy:
// it makes the moby client speak HTTP, which our custom dialer then carries over
// ssh. No connection is established until the first API call.
func NewRemote(spec HostSpec) (*Client, error) {
	dialer, err := newSSHDialer(spec)
	if err != nil {
		return nil, fmt.Errorf("build ssh dialer for %s: %w", spec.DockerHost, err)
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://placeholder:2375"),
		client.WithDialContext(dialer),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create remote docker client %s: %w", spec.DockerHost, err)
	}
	return &Client{cli: cli}, nil
}

// Ping verifies the daemon is reachable and returns its reported server version.
// Used by the host "test connection" endpoint.
func (c *Client) Ping(ctx context.Context) (string, error) {
	if _, err := c.cli.Ping(ctx, client.PingOptions{}); err != nil {
		return "", fmt.Errorf("ping docker daemon: %w", err)
	}
	ver, err := c.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", fmt.Errorf("server version: %w", err)
	}
	return ver.Version, nil
}
