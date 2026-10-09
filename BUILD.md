# Windows Build Guide

## Windows Prerequisites

### 1. Install Go 1.25+

Download and install from [go.dev/dl](https://go.dev/dl/), then verify:

```powershell
go version
```

### 2. Install Node.js 18+

Download and install the LTS version from [nodejs.org](https://nodejs.org/), then verify:

```powershell
node -v
npm -v
```

### 3. Install GCC (MSYS2)

SI depends on `mattn/go-sqlite3`, which requires CGO compilation, so a C compiler is necessary.

1. Download and install MSYS2 from [msys2.org](https://www.msys2.org/)
2. Open the **MSYS2 MSYS** terminal and run:

```bash
pacman -Syu
pacman -S mingw-w64-x86_64-gcc
```

3. Add the MinGW-w64 **`bin` directory that contains `gcc.exe`** to your system `Path`. That location depends on where MSYS2 (or another MinGW-w64 toolchain) was installed—for a default MSYS2 layout it is often `<MSYS2 root>\mingw64\bin` (for example `C:\msys64\mingw64\bin` if MSYS2 was installed to `C:\msys64`).
   - Right-click "This PC" → Properties → Advanced system settings → Environment Variables
   - Find `Path` in "System variables", edit it, and add that `bin` directory
   - Restart your terminal for the PATH to take effect

Verify:

```powershell
where.exe gcc
gcc --version
```

### 4. Allow Go’s CGO toolchain to run

The Windows build uses CGO (`CGO_ENABLED=1` and the `sqlite_fts5` build tag). In addition to a working `gcc`, the Go installation’s **`cgo` tool must be allowed to execute** (typically `cgo.exe` under `GOROOT\pkg\tool\<GOOS>_<GOARCH>\`).

On some machines, **Windows Application Control** (including Smart App Control, WDAC, or AppLocker) blocks that binary. When that happens, `go build` fails before compiling project code, with an error similar to:

```text
go: error obtaining buildID for go tool cgo: fork/exec ...\cgo.exe: An Application Control policy has blocked this file.
```

Local builds require that policy environment to permit Go’s `cgo` tool.

## Build

```powershell
# Clone or copy the source code to your local machine, then enter the project directory
cd session-insight

# Build the frontend
cd frontend
npm ci
npm run build
cd ..

# Copy frontend assets to the Go embed directory
xcopy /E /Y frontend\dist internal\web\dist\

# Compile (CGO must be enabled)
set CGO_ENABLED=1
go build -tags sqlite_fts5 -o session-insight.exe .
```

After the build completes, `session-insight.exe` is a complete executable with the frontend embedded in the binary.

## Run

```powershell
.\session-insight.exe
```

By default, it listens on `http://127.0.0.1:8080` and will automatically open your browser.

You can configure it via environment variables:

```powershell
$env:PORT = "9090"
$env:SI_INDEXER_ENABLED = "0" # linked worktrees only; the primary checkout always enables it
$env:AGENT_DIRS = "C:\Users\YourName\.chrys\sessions;C:\Users\YourName\.claude\projects"
.\session-insight.exe
```

`SI_INDEXER_ENABLED` accepts `1`, `true`, `yes`, or `on` to enable indexing, and
`0`, `false`, `no`, or `off` to disable it. An unset or invalid value falls
back to enabled. In a linked worktree, disabled mode leaves the local index
stale and disables source watchers, initial indexing, and periodic
reconciliation. The primary checkout always forces indexing enabled.

On start the binary opens the actual bound URL (`http://127.0.0.1:<port>/`), including when `PORT` was busy and an OS-assigned port was used instead.

## Troubleshooting

**`gcc: not found` or `exec: "gcc": executable file not found`**

MinGW-w64 `gcc` is not on your PATH. Confirm the `bin` directory that contains `gcc.exe` is on the system `Path`, then restart the terminal. Use `where.exe gcc` to see which binary (if any) is resolved.

**`sqlite3-binding.c: fatal error C1083: Cannot open include file: 'stdio.h'`**

This indicates you're using MSVC instead of mingw-w64's gcc. Ensure the MinGW-w64 `bin` path comes before MSVC in your PATH, or run the build commands in the MSYS2 MinGW 64-bit terminal.

**`An Application Control policy has blocked this file` (often for `cgo.exe`)**

Windows Application Control is preventing Go’s `cgo` tool from running. See [Allow Go’s CGO toolchain to run](#4-allow-gos-cgo-toolchain-to-run). This is a host policy constraint, not a missing project dependency.

**`npm run build` fails**

Try deleting `frontend/node_modules` and running `npm ci` again.

## Desktop Window Mode (`--app`)

Every binary is the same app: by default it serves the UI and opens your
browser. Pass `--app` to open the UI in a standalone desktop window backed by
the OS webview engine (WebKitGTK on Linux, WebKit on macOS, WebView2 on
Windows). The server, database, and indexing behavior are identical; closing
the window exits the process.

### Runtime requirements for `--app`

- **macOS**: none — WebKit is part of the OS.
- **Windows**: WebView2 runtime, preinstalled on Windows 10 1803+ and
  Windows 11.
- **Linux**: WebKitGTK (`sudo pacman -S webkit2gtk-4.1` or
  `sudo apt install libwebkit2gtk-4.1-0`). The binary does **not** require it
  otherwise: the GTK/WebKit symbols are linked weakly and the library is
  dlopen()ed on demand when `--app` runs. Without it, `--app` logs an install
  hint and falls back to the browser.

### Build-time requirements on Linux

Compiling the webview C++ backend needs the GTK/WebKit **headers**, so any
Linux build (with or without `--app` support in the final binary) requires:

- Arch: `sudo pacman -S webkit2gtk-4.1`
- Debian/Ubuntu: `sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev`

`run.sh` and `scripts/start.sh` source `scripts/buildtools/linux-cgo-env.sh`
automatically on Linux. It wires the pkg-config wrapper (forwards the removed
`webkit2gtk-4.0` lookup to 4.1, and strips webview link flags so the stack is
not recorded as a startup dependency) and force-includes the generated
`scripts/buildtools/weak-gtk-symbols.h` that declares the referenced GTK/WebKit
symbols weak. When the pinned `webview_go` version changes, regenerate that
header with:

```bash
bash scripts/buildtools/gen-webview-stubs.sh
```

If you build outside `run.sh`/`start.sh` on Linux, source
`scripts/buildtools/linux-cgo-env.sh` first (or set the same environment).

### Immutable-OS hosts (e.g. SteamOS)

Some immutable distributions strip C library/GTK dev files from `/usr` and
require root to install packages. Set `SI_APP_SYSROOT` to a user-space
sysroot containing the missing headers and `.pc` files (for example one
assembled from the distribution's exact package versions); `run.sh`,
`scripts/start.sh`, and the generator script pick it up and redirect
pkg-config/CGO into it while the produced binary still links dynamically
against the host runtime libraries:

```bash
SI_APP_SYSROOT=~/path/to/sysroot bash run.sh all
```
