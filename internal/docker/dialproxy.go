package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"stackd/internal/execpg"
)

// dialProxySpec is the fully-resolved input for one dial-stdio proxy. It is
// derived from a HostSpec (see dialProxySpecFromHost) and is what the proxy's
// per-connection ssh command (via buildSSHArgs) consumes.
type dialProxySpec struct {
	User           string // ssh user (may be empty; ssh uses its default)
	Host           string // ssh host (required)
	Port           string // ssh port (may be empty; ssh uses 22)
	KeyPath        string // decrypted private key file (may be empty)
	KnownHostsFile string // managed known_hosts path (may be empty)
	DockerPath     string // remote docker command (default "docker")
	LocalSocket    string // local unix socket the proxy listens on (required)
}

// dialProxySpecFromHost derives a dialProxySpec from a HostSpec, parsing the ssh
// destination out of DockerHost and applying the default docker path. It is pure
// (no I/O) so the mapping can be unit-tested.
func dialProxySpecFromHost(spec HostSpec) (dialProxySpec, error) {
	user, host, port, err := parseSSHHost(spec.DockerHost)
	if err != nil {
		return dialProxySpec{}, err
	}
	if spec.LocalSocket == "" {
		return dialProxySpec{}, fmt.Errorf("dial-stdio transport for host %s requires a local socket path", spec.ID)
	}
	dockerPath := spec.DockerPath
	if dockerPath == "" {
		dockerPath = DefaultDockerPath
	}
	return dialProxySpec{
		User:           user,
		Host:           host,
		Port:           port,
		KeyPath:        spec.KeyPath,
		KnownHostsFile: spec.KnownHostsFile,
		DockerPath:     dockerPath,
		LocalSocket:    spec.LocalSocket,
	}, nil
}

// sshArgs builds the argv (after "ssh") for one `docker system dial-stdio` over
// ssh, honouring the configured (possibly absolute) dockerPath. Kept as a method
// so tests can assert the argv from a spec.
func (s dialProxySpec) sshArgs() []string {
	return buildSSHArgs(s.User, s.Host, s.Port, s.KeyPath, s.KnownHostsFile, s.DockerPath)
}

// dialProxy is a supervised, long-lived local unix socket proxy for one remote
// host reached via `docker system dial-stdio`. It listens on LocalSocket and, for
// every accepted connection, spawns `ssh … <dockerPath> system dial-stdio` and
// bridges bytes both ways. This unifies dial-stdio with the forward transport:
// both the moby client and `docker compose` talk to unix://<LocalSocket>, and the
// remote command uses our configurable dockerPath (so it works on hosts such as
// Synology where docker is absent from the non-interactive SSH PATH).
//
// Lifecycle mirrors Tunnel: the supervisor keeps the listener running (restarting
// it with capped backoff on unexpected exit); readiness is verified by a Docker
// Ping over the local socket. The socket path is stable, so a supervisor restart
// does not invalidate an already-built docker client.
type dialProxy struct {
	hostID   string
	hostName string
	spec     dialProxySpec

	mu      sync.Mutex
	started bool
	stop    context.CancelFunc // cancels the supervisor (kills in-flight ssh groups)
	supDone chan struct{}      // closed when the supervisor goroutine exits
}

// Start ensures the supervisor is running and blocks until the proxy is ready (a
// Docker Ping over the local socket succeeds) or the readiness timeout / ctx
// elapses. It is idempotent: a second concurrent caller does not launch a
// duplicate listener, it just waits for readiness.
func (p *dialProxy) Start(ctx context.Context) error {
	p.mu.Lock()
	if !p.started {
		p.started = true
		supCtx, cancel := context.WithCancel(context.Background())
		p.stop = cancel
		p.supDone = make(chan struct{})
		go p.supervise(supCtx)
	}
	p.mu.Unlock()
	return waitSocketReady(ctx, p.hostID, p.spec.LocalSocket)
}

// Stop tears the proxy down: it cancels the supervisor (which closes the listener
// and, via execpg, SIGKILLs every in-flight ssh process group so no PID leaks),
// waits for it to exit, and removes the local socket file. Safe to call on a
// never-started proxy.
func (p *dialProxy) Stop() {
	p.mu.Lock()
	stop := p.stop
	done := p.supDone
	p.started = false
	p.stop = nil
	p.supDone = nil
	p.mu.Unlock()

	if stop != nil {
		stop()
	}
	if done != nil {
		<-done
	}
	_ = os.Remove(p.spec.LocalSocket)
}

// supervise keeps the local listener alive, restarting it with capped
// exponential backoff on unexpected exit until the context is cancelled by Stop.
func (p *dialProxy) supervise(ctx context.Context) {
	defer close(p.supDone)
	backoff := tunnelMinBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := p.runOnce(ctx)
		if ctx.Err() != nil {
			return // intentional Stop
		}
		slog.Warn("dial-stdio proxy exited, restarting",
			"host", p.hostID, "name", p.hostName, "err", err)
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

// runOnce binds the local unix socket and serves the accept loop until the
// listener fails or the context is cancelled. It clears any stale socket before
// binding and after exit so a dead proxy never leaves a socket that would fool
// readiness checks.
func (p *dialProxy) runOnce(ctx context.Context) error {
	_ = os.Remove(p.spec.LocalSocket)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", p.spec.LocalSocket)
	if err != nil {
		return fmt.Errorf("listen on %s for host %s: %w", p.spec.LocalSocket, p.hostID, err)
	}
	// Accept does not observe context cancellation, so close the listener when the
	// supervisor context is cancelled (or when runOnce returns) to unblock it.
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-closed:
		}
		_ = ln.Close()
	}()
	defer close(closed)

	slog.Info("starting dial-stdio proxy",
		"host", p.hostID, "name", p.hostName, "transport", TransportDialStdio,
		"localSocket", p.spec.LocalSocket, "dockerPath", p.spec.DockerPath)

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			wg.Wait() // let in-flight bridges drain (Stop cancels ctx → they're killed)
			_ = os.Remove(p.spec.LocalSocket)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accept on %s for host %s: %w", p.spec.LocalSocket, p.hostID, err)
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			p.handleConn(ctx, c)
		}(conn)
	}
}

// handleConn spawns one `ssh … dial-stdio` for an accepted connection and bridges
// bytes both directions until either side closes, then kills and reaps the ssh
// process group. The ssh command is bound to a per-connection context derived
// from the supervisor context, so it dies both when the connection ends and when
// the proxy is stopped — execpg SIGKILLs the whole group, so no PID leaks.
func (p *dialProxy) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := execpg.CommandContext(connCtx, "ssh", p.spec.sshArgs()...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		slog.Warn("dial-stdio proxy: ssh stdin pipe", "host", p.hostID, "err", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Warn("dial-stdio proxy: ssh stdout pipe", "host", p.hostID, "err", err)
		return
	}
	// Surface ssh diagnostics (auth failures, host-key issues) on stackd's stderr.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		slog.Warn("dial-stdio proxy: start ssh", "host", p.hostID, "err", err)
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stdin, conn) // client -> remote docker
		_ = stdin.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, stdout) // remote docker -> client
		done <- struct{}{}
	}()

	<-done           // one direction ended
	cancel()         // kill the ssh group so the other Copy unblocks (closes stdout)
	_ = conn.Close() // unblock the client->remote Copy (conn.Read returns)
	<-done           // both directions ended: safe to reap
	_ = cmd.Wait()
}
