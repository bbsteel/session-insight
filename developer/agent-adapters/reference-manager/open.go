package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// launchFolderManager starts the desktop file manager on dir. Tests replace
// this so HTTP handlers can be checked without opening a window.
var launchFolderManager = launchFolderManagerOS

var (
	lookOpenPath     = exec.LookPath
	startOpenCommand = func(cmd *exec.Cmd) error {
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	openRuntimeGOOS = runtime.GOOS
)

func validateWorkOrderID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("work order id is required")
	}
	if id != filepath.Clean(id) || id != filepath.Base(id) || id == "." || id == ".." {
		return fmt.Errorf("invalid work order id")
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid work order id")
	}
	return nil
}

// confinedWorkOrderDir resolves id to an existing directory under this
// checkout's .runtime/reference-work. The ID is the only path input; catalog
// Dir fields and request paths cannot point elsewhere.
func confinedWorkOrderDir(checkoutDir, id string) (string, error) {
	if err := validateWorkOrderID(id); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(workOrderRoot(checkoutDir))
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, id))
	if err != nil {
		return "", err
	}
	if !pathIsWithin(rootAbs, targetAbs) {
		return "", fmt.Errorf("work order path is outside the work-order root")
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return "", fmt.Errorf("work order directory not found")
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("work order directory not found")
	}
	info, err := os.Stat(resolvedTarget)
	if err != nil {
		return "", fmt.Errorf("work order directory not found")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work order path is not a directory")
	}
	if !pathIsWithin(resolvedRoot, resolvedTarget) {
		return "", fmt.Errorf("work order path is outside the work-order root")
	}
	return resolvedTarget, nil
}

func pathIsWithin(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return false
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(target+sep, root+sep)
}

func launchFolderManagerOS(dir string) error {
	switch openRuntimeGOOS {
	case "windows":
		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", dir)
		cmd.Stdout = nil
		cmd.Stderr = nil
		return startOpenCommand(cmd)
	case "darwin":
		cmd := exec.Command("open", dir)
		cmd.Stdout = nil
		cmd.Stderr = nil
		return startOpenCommand(cmd)
	default:
		names := []string{"dolphin", "nautilus", "nemo", "thunar", "pcmanfm", "xdg-open"}
		var launchErrs []string
		for _, name := range names {
			bin, err := lookOpenPath(name)
			if err != nil {
				continue
			}
			cmd := exec.Command(bin, dir)
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := startOpenCommand(cmd); err != nil {
				launchErrs = append(launchErrs, name+": "+err.Error())
				continue
			}
			return nil
		}
		if len(launchErrs) == 0 {
			return fmt.Errorf("no folder opener found (tried dolphin, nautilus, xdg-open)")
		}
		return fmt.Errorf("%s", strings.Join(launchErrs, "; "))
	}
}
