//go:build !windows

package sfcli

import (
	"os/exec"
	"syscall"
)

// killProcessGroup puts the child in its own process group so cancelling the
// command also kills the helpers `sf` spawns, rather than orphaning them.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid signals the whole group.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
