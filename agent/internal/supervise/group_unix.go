//go:build unix

package supervise

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the whole tree
// can be signalled together.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup signals the process group led by pid.
//
// 🚨 The negative pid is the point: syscall.Kill(-pid, sig) reaches every
// process in the group. Signalling pid alone leaves a forked JVM or wrapper
// script holding the game's port, and the next start fails with "address
// already in use" — which reads as a Garrison bug and is not one.
func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil {
		// No group (the child called setsid, or setpgid raced): fall back to
		// the process itself rather than giving up.
		return syscall.Kill(pid, sig)
	}
	return nil
}
