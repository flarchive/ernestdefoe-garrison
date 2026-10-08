//go:build windows

package supervise

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup is a no-op stub: Windows job objects are the equivalent and
// belong with the `service` driver, which is not in this phase.
func setProcessGroup(cmd *exec.Cmd) {}

func signalGroup(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
