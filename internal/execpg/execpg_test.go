//go:build unix

package execpg

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCancelKillsProcessGroup verifies that cancelling the context kills not
// just the direct child but any grandchildren it spawned.
func TestCancelKillsProcessGroup(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Shell forks a long-running sleep grandchild and prints its PID.
	// The shell itself then waits, so killing only the shell would leave
	// the sleep process orphaned — exactly the bug we are guarding against.
	cmd := CommandContext(ctx, "/bin/sh", "-c", "sleep 60 & echo $! ; wait")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Read the grandchild PID from the first line of stdout.
	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	pidStr := strings.TrimSpace(strings.SplitN(string(buf[:n]), "\n", 2)[0])
	grandchildPID, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("parse pid %q: %v", pidStr, err)
	}

	// Sanity: grandchild should be alive right now.
	if err := syscall.Kill(grandchildPID, 0); err != nil {
		t.Fatalf("grandchild %d not alive after start: %v", grandchildPID, err)
	}

	cancel()
	_ = cmd.Wait() // expect non-nil err (killed); we only care about exit.

	// Wait up to 3s for the kernel to reap the grandchild PID.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(grandchildPID, 0); err != nil {
			return // ESRCH — gone, success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild PID %d still alive 3s after cancel — process group not killed", grandchildPID)
}

// TestWaitDelayDefault checks that CommandContext sets WaitDelay so callers
// don't accidentally inherit the zero-value (block forever) behaviour.
func TestWaitDelayDefault(t *testing.T) {
	cmd := CommandContext(context.Background(), "true")
	if cmd.WaitDelay <= 0 {
		t.Fatalf("expected WaitDelay > 0, got %v", cmd.WaitDelay)
	}
}

// TestCancelBeforeStart returns a sensible error rather than panicking.
func TestCancelBeforeStart(t *testing.T) {
	cmd := CommandContext(context.Background(), "true")
	if cmd.Cancel == nil {
		t.Skip("Cancel not configured on this platform")
	}
	if err := cmd.Cancel(); err == nil {
		t.Fatal("expected Cancel to error before Start")
	} else if !strings.Contains(fmt.Sprint(err), "execpg") {
		t.Fatalf("unexpected error: %v", err)
	}
}
