package docker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/moby/moby/client"
	"stackd/internal/execpg"
)

// tunnelSpec is the fully-resolved input for one ssh socket-forward. It is built
// from a HostSpec (see forwardTunnelSpec) and is what sshForwardArgs consumes.
type tunnelSpec struct {
	User           string // ssh user (may be empty; ssh uses its default)
	Host           string // ssh host (required)
	Port           string // ssh port (may be empty; ssh uses 22)
	KeyPath        string // decrypted private key file (may be empty)
	KnownHostsFile string // managed known_hosts path (may be empty)
	LocalSocket    string // local unix socket the forward binds to (required)
	RemoteSocket   string // remote Docker socket path (required)
}

// Tunnel readiness / supervision tuning. Package vars (not consts) so tests can
// shorten them; production code never mutates them.
var (
	readinessTimeout      = 15 * time.Second
	readinessPollInterval = 200 * time.Millisecond
	readinessPingTimeout  = 3 * time.Second
	tunnelMinBackoff      = 500 * time.Millisecond
	tunnelMaxBackoff      = 30 * time.Second
	// backoffResetAfter: if a tunnel ran at least this long before exiting, the
	// restart backoff is reset (it was a healthy tunnel that dropped, not a
	// crash-loop).
	backoffResetAfter = 60 * time.Second
)

// sshForwardArgs builds the argv (after "ssh") for a long-lived local socket
// forward: it binds LocalSocket locally and forwards it to RemoteSocket on the
// remote host. It is pure so the argv can be asserted in tests.
//
//   - -N: do not run a remote command (pure forward)
//   - -T: no pseudo-terminal
//   - ExitOnForwardFailure: fail fast (and let the supervisor restart) if the
//     forward cannot be established, instead of a silently dead tunnel
//   - ServerAlive*: detect a dropped connection so ssh exits and is restarted
//
// The destination (user@host or host) is placed LAST with no trailing tokens, so
// ssh never interprets anything after it as a remote command. keyPath, knownHosts
// and port are omitted when empty. Callers must validate user/host/port to
// contain no leading '-' or control characters (see parseSSHHost).
func sshForwardArgs(spec tunnelSpec) []string {
	args := []string{"-N", "-T"}
	if spec.KeyPath != "" {
		args = append(args, "-i", spec.KeyPath)
	}
	args = append(args, "-o", "StrictHostKeyChecking=accept-new")
	if spec.KnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+spec.KnownHostsFile)
	}
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
	)
	if spec.Port != "" {
		args = append(args, "-p", spec.Port)
	}
	args = append(args, "-L", spec.LocalSocket+":"+spec.RemoteSocket)
	dest := spec.Host
	if spec.User != "" {
		dest = spec.User + "@" + spec.Host
	}
	args = append(args, dest)
	return args
}

// forwardTunnelSpec derives a tunnelSpec from a HostSpec, parsing the ssh
// destination out of DockerHost and applying the default remote socket. It is
// pure (no I/O) so the mapping can be unit-tested.
func forwardTunnelSpec(spec HostSpec) (tunnelSpec, error) {
	user, host, port, err := parseSSHHost(spec.DockerHost)
	if err != nil {
		return tunnelSpec{}, err
	}
	if spec.LocalSocket == "" {
		return tunnelSpec{}, fmt.Errorf("forward transport for host %s requires a local socket path", spec.ID)
	}
	remote := spec.RemoteSocket
	if remote == "" {
		remote = DefaultRemoteSocket
	}
	return tunnelSpec{
		User:           user,
		Host:           host,
		Port:           port,
		KeyPath:        spec.KeyPath,
		KnownHostsFile: spec.KnownHostsFile,
		LocalSocket:    spec.LocalSocket,
		RemoteSocket:   remote,
	}, nil
}

// forwardClientHost is the moby client host string for a forwarded socket. Pure,
// for tests and callers.
func forwardClientHost(localSocket string) string { return "unix://" + localSocket }

// ForwardComposeDockerHost is the DOCKER_HOST value a `docker compose` subprocess
// uses to talk to a forwarded local socket. It is the same unix:// URL the moby
// client dials, exposed for the resolver's ComposeEnv.
func ForwardComposeDockerHost(localSocket string) string { return forwardClientHost(localSocket) }

// Tunnel is a supervised, long-lived ssh local-forward for one remote host. The
// supervisor keeps the ssh process running (restarting it with capped backoff on
// unexpected exit); readiness is verified independently by Start via a Docker
// Ping over the forwarded local socket. Because the local socket path is stable,
// a transparent supervisor restart does not invalidate an already-built docker
// client — it simply redials the same path on its next request.
type Tunnel struct {
	hostID   string
	hostName string
	spec     tunnelSpec

	mu      sync.Mutex
	started bool
	stop    context.CancelFunc // cancels the supervisor (kills the ssh group)
	supDone chan struct{}      // closed when the supervisor goroutine exits
}

// Start ensures the supervisor is running and blocks until the tunnel is ready
// (the local socket accepts a Docker Ping) or the readiness timeout / ctx
// elapses. It is idempotent: a second concurrent caller does not launch a
// duplicate ssh process, it just waits for readiness.
func (t *Tunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	if !t.started {
		t.started = true
		supCtx, cancel := context.WithCancel(context.Background())
		t.stop = cancel
		t.supDone = make(chan struct{})
		go t.supervise(supCtx)
	}
	t.mu.Unlock()
	return t.waitReady(ctx)
}

// Stop tears the tunnel down: it cancels the supervisor (execpg SIGKILLs the
// whole ssh process group, so no PID leak), waits for it to exit, and removes the
// local socket file. Safe to call on a never-started tunnel.
func (t *Tunnel) Stop() {
	t.mu.Lock()
	stop := t.stop
	done := t.supDone
	t.started = false
	t.stop = nil
	t.supDone = nil
	t.mu.Unlock()

	if stop != nil {
		stop()
	}
	if done != nil {
		<-done
	}
	_ = os.Remove(t.spec.LocalSocket)
}

// supervise keeps the ssh forward alive, restarting it with capped exponential
// backoff on unexpected exit until the context is cancelled by Stop.
func (t *Tunnel) supervise(ctx context.Context) {
	defer close(t.supDone)
	backoff := tunnelMinBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := t.runOnce(ctx)
		if ctx.Err() != nil {
			return // intentional Stop
		}
		slog.Warn("ssh socket-forward tunnel exited, restarting",
			"host", t.hostID, "name", t.hostName, "err", err)
		if time.Since(started) >= backoffResetAfter {
			backoff = tunnelMinBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < tunnelMaxBackoff {
			backoff *= 2
			if backoff > tunnelMaxBackoff {
				backoff = tunnelMaxBackoff
			}
		}
	}
}

// runOnce launches one ssh forward and blocks until it exits. On context cancel
// execpg kills the whole ssh process group. It clears any stale socket before
// launch and after exit so a dead tunnel never leaves a socket that would fool
// readiness checks.
func (t *Tunnel) runOnce(ctx context.Context) error {
	_ = os.Remove(t.spec.LocalSocket)
	cmd := execpg.CommandContext(ctx, "ssh", sshForwardArgs(t.spec)...)
	cmd.Stderr = os.Stderr
	slog.Info("starting ssh socket-forward tunnel",
		"host", t.hostID, "name", t.hostName, "transport", TransportForward,
		"localSocket", t.spec.LocalSocket, "remoteSocket", t.spec.RemoteSocket)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ssh forward for host %s: %w", t.hostID, err)
	}
	err := cmd.Wait()
	_ = os.Remove(t.spec.LocalSocket)
	return err
}

// waitReady polls until the forwarded socket answers a Docker Ping or the
// readiness timeout (or ctx) elapses.
func (t *Tunnel) waitReady(ctx context.Context) error {
	return waitSocketReady(ctx, t.hostID, t.spec.LocalSocket)
}

// waitSocketReady polls until a local unix socket exists AND answers a Docker
// Ping, or the readiness timeout (or ctx) elapses. It is shared by the forward
// tunnel and the dial-stdio proxy, whose readiness is defined identically: the
// moby client can reach a daemon over unix://<localSocket>.
func waitSocketReady(ctx context.Context, hostID, localSocket string) error {
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	ticker := time.NewTicker(readinessPollInterval)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(localSocket); err == nil {
			pingCtx, pcancel := context.WithTimeout(ctx, readinessPingTimeout)
			perr := pingUnixSocket(pingCtx, localSocket)
			pcancel()
			if perr == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("socket for host %s not ready within %s: %w", hostID, readinessTimeout, ctx.Err())
		case <-ticker.C:
		}
	}
}

// pingUnixSocket builds a throwaway Docker client over the local unix socket and
// pings it. Used only for readiness probing.
func pingUnixSocket(ctx context.Context, sock string) error {
	cli, err := client.NewClientWithOpts(client.WithHost(forwardClientHost(sock)))
	if err != nil {
		return err
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		return err
	}
	return nil
}

// supervised is a long-lived, per-host ssh transport that exposes a local unix
// socket: either a forward tunnel (*Tunnel) or a dial-stdio proxy (*dialProxy).
// Both start a supervised background process on Start and tear it down on Stop,
// so the manager can own them uniformly.
type supervised interface {
	Start(ctx context.Context) error
	Stop()
}

// TunnelManager owns the set of live ssh transports (forward tunnels and
// dial-stdio proxies), keyed by host id, so they are shared across the app and
// cleaned up on shutdown. It starts them lazily (Ensure / EnsureProxy) and tears
// them down on host invalidate (Stop) or shutdown (StopAll). A host uses exactly
// one transport at a time, so its id maps to a single entry.
type TunnelManager struct {
	mu    sync.Mutex
	conns map[string]supervised
}

// NewTunnelManager returns an empty manager.
func NewTunnelManager() *TunnelManager {
	return &TunnelManager{conns: make(map[string]supervised)}
}

// Ensure returns a ready forward tunnel for hostID, creating and starting it on
// first use. The supervisor is launched at most once per host id; concurrent
// callers share the same tunnel and all block until it is ready.
func (m *TunnelManager) Ensure(ctx context.Context, hostID, hostName string, spec tunnelSpec) (*Tunnel, error) {
	m.mu.Lock()
	t, _ := m.conns[hostID].(*Tunnel)
	created := false
	if t == nil {
		t = &Tunnel{hostID: hostID, hostName: hostName, spec: spec}
		m.conns[hostID] = t
		created = true
	}
	m.mu.Unlock()

	if err := t.Start(ctx); err != nil {
		// Don't leave a supervisor retrying in the background for a host that never
		// became ready (unreachable, bad key, refused forwarding). Only the caller
		// that created this entry tears it down, and only if it's still the one in
		// the map (a later Ensure may have replaced it).
		if created {
			m.remove(hostID, t)
		}
		return nil, err
	}
	return t, nil
}

// EnsureProxy returns a ready dial-stdio proxy for hostID, creating and starting
// it on first use. Like Ensure, the supervisor is launched at most once per host
// id and concurrent callers share the same proxy.
func (m *TunnelManager) EnsureProxy(ctx context.Context, hostID, hostName string, spec dialProxySpec) (*dialProxy, error) {
	m.mu.Lock()
	p, _ := m.conns[hostID].(*dialProxy)
	created := false
	if p == nil {
		p = &dialProxy{hostID: hostID, hostName: hostName, spec: spec}
		m.conns[hostID] = p
		created = true
	}
	m.mu.Unlock()

	if err := p.Start(ctx); err != nil {
		if created {
			m.remove(hostID, p)
		}
		return nil, err
	}
	return p, nil
}

// remove deletes entry from the map only if it is still the current one for
// hostID, then stops it. Safe against a concurrent Ensure that replaced it.
func (m *TunnelManager) remove(hostID string, entry supervised) {
	m.mu.Lock()
	if m.conns[hostID] == entry {
		delete(m.conns, hostID)
	}
	m.mu.Unlock()
	entry.Stop()
}

// Stop tears down and forgets the transport for hostID (e.g. on host invalidate).
func (m *TunnelManager) Stop(hostID string) {
	m.mu.Lock()
	c := m.conns[hostID]
	delete(m.conns, hostID)
	m.mu.Unlock()
	if c != nil {
		c.Stop()
	}
}

// StopAll tears down every transport. Intended for shutdown.
func (m *TunnelManager) StopAll() {
	m.mu.Lock()
	cs := m.conns
	m.conns = make(map[string]supervised)
	m.mu.Unlock()
	for _, c := range cs {
		c.Stop()
	}
}
