package docker

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/moby/moby/client"
)

// Transport selects how a remote Docker daemon is reached over ssh.
const (
	// TransportForward (default) forwards the remote Docker unix socket over an
	// ssh local-forward (ssh -L) and talks to it locally via unix://<localSocket>.
	// It needs only socket access + ssh, no `docker` binary on the remote — but it
	// requires the remote sshd to permit stream-local (unix-socket) forwarding,
	// which some locked-down hosts (e.g. Synology) refuse.
	TransportForward = "forward"
	// TransportDialStdio runs `ssh … <dockerPath> system dial-stdio` behind a local
	// unix-socket proxy, so it also exposes unix://<localSocket> (both the API
	// client and `docker compose` use it). It needs the docker CLI reachable on the
	// remote — either on the non-interactive SSH PATH or via an absolute DockerPath
	// (e.g. /usr/local/bin/docker) — but only an ssh exec channel, so it works on
	// hosts that refuse socket forwarding (e.g. Synology).
	TransportDialStdio = "dial-stdio"
	// DefaultRemoteSocket is the Docker socket path assumed on the remote host when
	// none is configured.
	DefaultRemoteSocket = "/var/run/docker.sock"
	// DefaultDockerPath is the remote docker command used for dial-stdio when none
	// is configured. An absolute path (e.g. /usr/local/bin/docker) lets restrictive
	// hosts (Synology) work without docker on the non-interactive SSH PATH.
	DefaultDockerPath = "docker"
)

// HostSpec describes how to reach a Docker daemon. An empty DockerHost selects
// the local socket (see Registry.Get); otherwise DockerHost is
// "ssh://user@host[:port]" and the connection is either a socket forward
// (Transport "forward") or a dial-stdio tunnel (Transport "dial-stdio").
type HostSpec struct {
	ID             string // host id used as the registry cache key (e.g. "local" or a uuid)
	DockerHost     string // "" = local socket; else ssh://user@host[:port]
	KeyPath        string // path to the decrypted private key file (remote only)
	KnownHostsFile string // managed known_hosts path (remote only)

	// Transport is "forward" (default) or "dial-stdio"; ignored for the local
	// host. RemoteSocket is the Docker socket path on the remote host (forward
	// only). LocalSocket is the local unix socket path the forward binds to
	// (forward only) — the moby client dials unix://LocalSocket.
	Transport    string
	RemoteSocket string
	LocalSocket  string

	// DockerPath is the remote docker command for dial-stdio (default "docker").
	// Set an absolute path when docker is not on the remote's non-interactive SSH
	// PATH (e.g. Synology: /usr/local/bin/docker).
	DockerPath string
}

// ValidateTransport normalises and validates a transport value. An empty string
// maps to the default ("forward"). Any value other than the two known transports
// is rejected.
func ValidateTransport(s string) (string, error) {
	switch s {
	case "", TransportForward:
		return TransportForward, nil
	case TransportDialStdio:
		return TransportDialStdio, nil
	default:
		return "", fmt.Errorf("transport must be %q or %q, got %q", TransportForward, TransportDialStdio, s)
	}
}

// ValidateDockerPath normalises and validates the remote docker command used for
// dial-stdio. Empty maps to the default ("docker"). It is a single argv token, so
// it must not contain spaces or control characters, and must not begin with '-'
// (which ssh/the remote shell could treat as an option).
func ValidateDockerPath(s string) (string, error) {
	if s == "" {
		return DefaultDockerPath, nil
	}
	if strings.HasPrefix(s, "-") {
		return "", fmt.Errorf("docker path %q must not begin with '-'", s)
	}
	// The value is executed by the remote shell via ssh, so restrict it to a safe
	// path/command charset. This rejects shell metacharacters (; | & $ ` > < ( )
	// quotes, spaces, control chars) that would otherwise allow command injection
	// on the remote host, e.g. "/usr/local/bin/docker;id".
	for _, r := range s {
		safe := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '/' || r == '.' || r == '_' || r == '-'
		if !safe {
			return "", fmt.Errorf("docker path %q may only contain letters, digits, and / . _ -", s)
		}
	}
	return s, nil
}

// ValidateRemoteSocket normalises and validates a remote Docker socket path. An
// empty string maps to the default. It must be an absolute path and contain no
// control characters or spaces (which could break the ssh -L forward spec or
// smuggle options).
func ValidateRemoteSocket(s string) (string, error) {
	if s == "" {
		return DefaultRemoteSocket, nil
	}
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("remote socket %q must be an absolute path", s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("remote socket %q must not contain control characters", s)
		}
		if r == ' ' {
			return "", fmt.Errorf("remote socket %q must not contain spaces", s)
		}
	}
	return s, nil
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
// over `docker system dial-stdio`. It mirrors what docker/cli's connhelper does
// and is consumed by the dial-stdio proxy (see dialProxy), which spawns one such
// ssh process per accepted local-socket connection. It is pure so the argv can be
// asserted in tests. keyPath and knownHosts may be empty (the corresponding
// option is then omitted).
func buildSSHArgs(user, host, port, keyPath, knownHosts, dockerPath string) []string {
	if dockerPath == "" {
		dockerPath = DefaultDockerPath
	}
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
	args = append(args, "--", dest, dockerPath, "system", "dial-stdio")
	return args
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
