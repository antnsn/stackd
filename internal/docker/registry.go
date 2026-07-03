package docker

import (
	"context"
	"log/slog"
	"sync"
)

// Registry lazily builds and caches one *Client per host id. The local host
// (DockerHost == "") uses client.FromEnv; every other host uses an ssh
// dial-stdio tunnel (NewRemote).
//
// It replaces the single ClientHolder: a failed build is simply not cached, so
// the next Get retries — this preserves the old holder's reconnect behaviour for
// the local daemon (a daemon that comes back up is picked up on the next call).
type Registry struct {
	mu      sync.Mutex
	clients map[string]*Client
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{clients: make(map[string]*Client)}
}

// Get returns the cached client for spec.ID, building it on first use. A build
// error is returned to the caller and NOT cached, so a subsequently reachable
// host (or a restarted local daemon) is retried on the next call.
func (r *Registry) Get(ctx context.Context, spec HostSpec) (*Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.clients[spec.ID]; c != nil {
		return c, nil
	}
	var (
		c   *Client
		err error
	)
	if spec.DockerHost == "" {
		c, err = New()
	} else {
		c, err = NewRemote(spec)
	}
	if err != nil {
		return nil, err
	}
	r.clients[spec.ID] = c
	return c, nil
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
// host's connection details change or it is deleted). The next Get rebuilds it.
func (r *Registry) Invalidate(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.clients[id]; c != nil {
		if err := c.Close(); err != nil {
			slog.Debug("closing docker client on invalidate", "host", id, "err", err)
		}
		delete(r.clients, id)
	}
}

// CloseAll closes every cached client. Intended for shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.clients {
		if c != nil {
			_ = c.Close()
		}
		delete(r.clients, id)
	}
}
