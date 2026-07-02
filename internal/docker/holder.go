package docker

import "sync/atomic"

// ClientHolder holds a *Client that can be swapped atomically. Reconnects made
// via Set() are immediately visible to every goroutine calling Get(), which
// removes the data race caused by mutating a shared *Client through a pointer.
type ClientHolder struct {
	p atomic.Pointer[Client]
}

// NewHolder returns a holder wrapping c (which may be nil).
func NewHolder(c *Client) *ClientHolder {
	h := &ClientHolder{}
	if c != nil {
		h.p.Store(c)
	}
	return h
}

// Get returns the current client, or nil if none is set.
func (h *ClientHolder) Get() *Client {
	if h == nil {
		return nil
	}
	return h.p.Load()
}

// Set atomically replaces the current client.
func (h *ClientHolder) Set(c *Client) {
	if h == nil {
		return
	}
	h.p.Store(c)
}

// CompareAndSwap atomically publishes new when the current client is still old,
// returning true on success. It lets a reconnecting caller detect that another
// goroutine already installed a client so the loser can Close its own instead of
// leaking it. Reports false on a nil holder.
func (h *ClientHolder) CompareAndSwap(old, new *Client) bool {
	if h == nil {
		return false
	}
	return h.p.CompareAndSwap(old, new)
}
