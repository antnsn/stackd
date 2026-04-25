// Package execpg wraps os/exec so that subprocess trees are reliably terminated
// when their context is cancelled.
//
// The standard library's exec.CommandContext only sends SIGKILL to the direct
// child. Tools like git-over-ssh, docker compose, and infisical fork further
// children that survive that signal and become orphaned PIDs, eventually
// exhausting the per-user nproc limit (manifests as runtime panic
// "newosproc: errno=11 / EAGAIN" once Go can no longer create OS threads).
//
// CommandContext returned here:
//   - puts the child in its own process group (Setpgid),
//   - on context cancel sends SIGKILL to the entire group (negative pid),
//   - sets WaitDelay so Wait returns even if a grandchild keeps an output
//     pipe open after the kill.
package execpg

import (
	"context"
	"os/exec"
	"time"
)

// DefaultWaitDelay bounds how long Wait blocks after the context is cancelled
// while waiting for I/O pipes inherited by grandchildren to drain.
const DefaultWaitDelay = 5 * time.Second

// CommandContext is a drop-in replacement for exec.CommandContext that kills
// the entire process group on cancellation.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Configure(cmd)
	return cmd
}

// Configure applies the pgroup + cancel + wait-delay behaviour to an existing
// *exec.Cmd. Useful when callers need to construct the Cmd themselves.
func Configure(cmd *exec.Cmd) {
	configure(cmd)
}
