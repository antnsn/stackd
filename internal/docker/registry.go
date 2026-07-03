package docker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/moby/moby/client"
)

// Registry lazily builds and caches one *Client per host id. The local host
// (DockerHost == "") uses client.FromEnv; every remote host reaches its daemon
// over a local unix socket exposed by an ssh transport — either an `ssh -L`
// socket-forward tunnel (forward transport) or a dial-stdio proxy (dial-stdio
// transport). In both cases the moby client dials unix://<localSocket>.
//
// It replaces the single ClientHolder: a failed build is simply not cached, so
// the next Get retries — this preserves the old holder's reconnect behaviour for
// the local daemon (a daemon that comes back up is picked up on the next call).
type Registry struct {
	mu      sync.Mutex
	clients map[string]*Client
	tunnels *TunnelManager // owns the ssh transports (forward tunnels + dial-stdio proxies)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		clients: make(map[string]*Client),
		tunnels: NewTunnelManager(),
	}
}

// Get returns the cached client for spec.ID, building it on first use. A build
// error is returned to the caller and NOT cached, so a subsequently reachable
// host (or a restarted local daemon) is retried on the next call.
//
// For a forward-transport host, building the client first ensures the ssh
// socket-forward tunnel is up and ready; a failure to establish it surfaces to
// the caller (the stack shows an error rather than hanging).
func (r *Registry) Get(ctx context.Context, spec HostSpec) (*Client, error) {
	// The tunnel readiness wait for a forward host can take up to
	// readinessTimeout, so build outside the registry lock (which would otherwise
	// stall every other host). The lock is only held for the cache read/write.
	r.mu.Lock()
	if c := r.clients[spec.ID]; c != nil {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	var (
		c   *Client
		err error
	)
	switch {
	case spec.DockerHost == "":
		c, err = New()
	case spec.Transport == TransportDialStdio:
		c, err = r.newDialProxyClient(ctx, spec)
	default: // TransportForward (also the default when unset)
		c, err = r.newForwardClient(ctx, spec)
	}
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	// Another caller may have built + cached a client for this id while we were
	// building ours (we released the lock during the build). Prefer the cached one
	// and discard the duplicate so exactly one client per id is ever cached.
	if existing := r.clients[spec.ID]; existing != nil {
		r.mu.Unlock()
		_ = c.Close()
		return existing, nil
	}
	r.clients[spec.ID] = c
	r.mu.Unlock()
	return c, nil
}

// newForwardClient ensures the host's ssh socket-forward tunnel is ready, then
// builds a moby client that dials the forwarded local unix socket.
func (r *Registry) newForwardClient(ctx context.Context, spec HostSpec) (*Client, error) {
	if err := r.EnsureForwardReady(ctx, spec); err != nil {
		return nil, err
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost(forwardClientHost(spec.LocalSocket)),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create forward docker client %s: %w", spec.ID, err)
	}
	return &Client{cli: cli}, nil
}

// EnsureForwardReady ensures the ssh socket-forward tunnel for a forward-transport
// host is up and ready (a Docker Ping over the local socket succeeds). It is
// idempotent and safe to call on the hot path; the resolver uses it to guarantee
// readiness before handing a `docker compose` subprocess a DOCKER_HOST that
// points at the local socket.
func (r *Registry) EnsureForwardReady(ctx context.Context, spec HostSpec) error {
	tspec, err := forwardTunnelSpec(spec)
	if err != nil {
		return err
	}
	if _, err := r.tunnels.Ensure(ctx, spec.ID, spec.ID, tspec); err != nil {
		return fmt.Errorf("ensure tunnel for host %s: %w", spec.ID, err)
	}
	return nil
}

// newDialProxyClient ensures the host's dial-stdio proxy is ready, then builds a
// moby client that dials the proxy's local unix socket — exactly like
// newForwardClient. Unifying on unix://<localSocket> means the configurable
// dockerPath (used by the proxy's remote ssh command) applies to every path
// (API client and `docker compose`), removing the remote-PATH dependency.
func (r *Registry) newDialProxyClient(ctx context.Context, spec HostSpec) (*Client, error) {
	if err := r.EnsureDialProxyReady(ctx, spec); err != nil {
		return nil, err
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost(forwardClientHost(spec.LocalSocket)),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create dial-stdio proxy docker client %s: %w", spec.ID, err)
	}
	return &Client{cli: cli}, nil
}

// EnsureDialProxyReady ensures the dial-stdio proxy for a dial-stdio-transport
// host is up and ready (a Docker Ping over the local socket succeeds). Like
// EnsureForwardReady it is idempotent and safe on the hot path; the resolver uses
// it to guarantee readiness before handing `docker compose` a DOCKER_HOST that
// points at the local socket.
func (r *Registry) EnsureDialProxyReady(ctx context.Context, spec HostSpec) error {
	pspec, err := dialProxySpecFromHost(spec)
	if err != nil {
		return err
	}
	if _, err := r.tunnels.EnsureProxy(ctx, spec.ID, spec.ID, pspec); err != nil {
		return fmt.Errorf("ensure dial-stdio proxy for host %s: %w", spec.ID, err)
	}
	return nil
}

// Cached returns the already-built client for a host id, or nil if none is
// cached. It never builds a client, letting callers skip expensive spec
// preparation (key decryption / file writes) on the hot path.
func (r *Registry) Cached(id string) *Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clients[id]
}

// Invalidate closes and drops the cached client for a host id (e.g. after the
// host's connection details change or it is deleted) and tears down its ssh
// tunnel, if any. The next Get rebuilds both. The tunnel teardown blocks until
// the ssh process group exits, so it runs after the registry lock is released.
func (r *Registry) Invalidate(id string) {
	r.mu.Lock()
	c := r.clients[id]
	delete(r.clients, id)
	r.mu.Unlock()
	if c != nil {
		if err := c.Close(); err != nil {
			slog.Debug("closing docker client on invalidate", "host", id, "err", err)
		}
	}
	r.tunnels.Stop(id)
}

// CloseAll closes every cached client and stops every tunnel. Intended for
// shutdown. Tunnel teardown blocks on ssh exit, so it runs after the lock is
// released.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	clients := r.clients
	r.clients = make(map[string]*Client)
	r.mu.Unlock()
	for _, c := range clients {
		if c != nil {
			_ = c.Close()
		}
	}
	r.tunnels.StopAll()
}
