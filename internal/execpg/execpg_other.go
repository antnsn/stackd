//go:build !unix

package execpg

import "os/exec"

func configure(cmd *exec.Cmd) {
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = DefaultWaitDelay
	}
}
