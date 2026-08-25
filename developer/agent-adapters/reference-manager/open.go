package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
	id = strings.TrimSpace(id)
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
	rootHandle, err := os.OpenInRoot(rootAbs, id)
	if err != nil {
		return "", fmt.Errorf("work order directory not found")
	}
	defer rootHandle.Close() //nolint:errcheck
	info, err := rootHandle.Stat()
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
		return launchLinuxFolder(dir)
	}
}

func launchLinuxFolder(dir string) error {
	names := []string{"dolphin", "nautilus", "nemo", "thunar", "pcmanfm", "xdg-open"}
	var launchErrs []string
	for _, name := range names {
		bin, err := lookOpenPath(name)
		if err != nil {
			continue
		}
		args := folderManagerArgs(bin, dir)
		if err := launchInDesktopSession(bin, args); err != nil {
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

func folderManagerArgs(bin, dir string) []string {
	base := strings.ToLower(filepath.Base(bin))
	if base == "dolphin" {
		return []string{"--new-window", "--", dir}
	}
	return []string{dir}
}

// launchInDesktopSession starts bin in the graphical user session. A direct
// exec from this process (agent / sandbox / nohup) makes KDE Dolphin fail with
// "cannot create KIO worker" because kioworker cannot bind its sockets.
func launchInDesktopSession(bin string, args []string) error {
	if err := startSystemdUserCommand(bin, args); err == nil {
		return nil
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = desktopSessionEnv()
	cmd.Stdout = nil
	cmd.Stderr = nil
	return startOpenCommand(cmd)
}

func startSystemdUserCommand(bin string, args []string) error {
	if openRuntimeGOOS == "windows" || openRuntimeGOOS == "darwin" {
		return fmt.Errorf("systemd-run not used on this OS")
	}
	sr, err := lookOpenPath("systemd-run")
	if err != nil {
		return err
	}
	sessionEnv := desktopSessionEnv()
	srArgs := []string{"--user", "--quiet", "--collect"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		srArgs = append(srArgs, "--working-directory="+home)
	}
	for _, key := range desktopEnvKeys {
		if v := envLookup(sessionEnv, key); v != "" {
			srArgs = append(srArgs, "--setenv="+key+"="+v)
		}
	}
	srArgs = append(srArgs, "--", bin)
	srArgs = append(srArgs, args...)
	cmd := exec.Command(sr, srArgs...)
	cmd.Env = sessionEnv
	cmd.Stdout = nil
	cmd.Stderr = nil
	return startOpenCommand(cmd)
}

// desktopEnvKeys are the graphical-session variables KDE/Qt file managers
// need. Copied in spirit from the product open-file helper: this tool is
// often started without a full desktop bus.
var desktopEnvKeys = []string{
	"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR",
	"DBUS_SESSION_BUS_ADDRESS", "XDG_CURRENT_DESKTOP", "DESKTOP_SESSION",
	"KDE_FULL_SESSION", "KDE_SESSION_VERSION", "KDE_SESSION_UID",
	"KDE_APPLICATIONS_AS_SCOPE", "XDG_SESSION_TYPE", "XDG_SESSION_CLASS",
	"XDG_SEAT", "XDG_VTNR", "SESSION_MANAGER", "QT_QPA_PLATFORM",
	"QT_WAYLAND_RECONNECT", "XDG_CONFIG_DIRS", "XDG_DATA_DIRS",
	"HOME", "LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE",
}

func desktopSessionEnv() []string {
	env := os.Environ()
	put := func(key, val string) {
		if key == "" {
			return
		}
		prefix := key + "="
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = prefix + val
				return
			}
		}
		env = append(env, prefix+val)
	}
	if userEnv := loadUserSystemdEnv(); userEnv != nil {
		for _, key := range desktopEnvKeys {
			if v := userEnv[key]; v != "" {
				put(key, v)
			}
		}
	}
	uid := os.Getuid()
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	if st, err := os.Stat(runtimeDir); err == nil && st.IsDir() {
		put("XDG_RUNTIME_DIR", runtimeDir)
		busPath := filepath.Join(runtimeDir, "bus")
		if _, err := os.Stat(busPath); err == nil {
			put("DBUS_SESSION_BUS_ADDRESS", "unix:path="+busPath)
		}
		if cur := envLookup(env, "XAUTHORITY"); cur == "" {
			if matches, _ := filepath.Glob(filepath.Join(runtimeDir, "xauth_*")); len(matches) > 0 {
				put("XAUTHORITY", matches[0])
			}
		} else if _, err := os.Stat(cur); err != nil {
			if matches, _ := filepath.Glob(filepath.Join(runtimeDir, "xauth_*")); len(matches) > 0 {
				put("XAUTHORITY", matches[0])
			}
		}
		if envLookup(env, "WAYLAND_DISPLAY") == "" {
			if _, err := os.Stat(filepath.Join(runtimeDir, "wayland-0")); err == nil {
				put("WAYLAND_DISPLAY", "wayland-0")
			}
		}
	}
	if envLookup(env, "DISPLAY") == "" {
		if _, err := os.Stat("/tmp/.X11-unix/X0"); err == nil {
			put("DISPLAY", ":0")
		}
	}
	return env
}

func loadUserSystemdEnv() map[string]string {
	if openRuntimeGOOS == "windows" || openRuntimeGOOS == "darwin" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "show-environment").Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		m[key] = unquoteSystemdEnvValue(val)
	}
	return m
}

func unquoteSystemdEnvValue(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	if strings.HasPrefix(v, "$'") && strings.HasSuffix(v, "'") && len(v) >= 3 {
		return v[2 : len(v)-1]
	}
	return v
}

func envLookup(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}
