//go:build !windows

package llm

import (
	"os/exec"
	"syscall"
)

// setProcAttr puts the adapter in its own process group so killProc can take
// down npx together with the node adapter and agent CLI it spawns. Killing
// only npx leaves grandchildren holding our stderr pipe open, and cmd.Wait()
// then blocks forever waiting for pipe EOF.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProc(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
