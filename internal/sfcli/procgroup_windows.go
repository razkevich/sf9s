//go:build windows

package sfcli

import "os/exec"

// killProcessGroup is a no-op on Windows, which has no process groups in the
// POSIX sense. WaitDelay still bounds the wait on inherited pipes, which is
// what actually prevents the hang.
func killProcessGroup(cmd *exec.Cmd) {}
