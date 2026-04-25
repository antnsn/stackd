//go:build unix

package execpg

import (
	"errors"
	"os/exec"
	"syscall"
)

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return errors.New("execpg: process not started")
		}
		// Negative pid targets the whole process group, killing any
		// grandchildren (ssh, docker compose helpers, etc.) the direct
		// child may have spawned.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			// Fall back to killing just the child if the group lookup
			// failed (e.g. process already exited).
			return cmd.Process.Kill()
		}
		return nil
	}

	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = DefaultWaitDelay
	}
}
