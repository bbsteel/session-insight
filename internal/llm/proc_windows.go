//go:build windows

package llm

import "os/exec"

func setProcAttr(cmd *exec.Cmd) {}

func killProc(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
