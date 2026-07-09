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

3. Add `C:\msys64\mingw64\bin` to your system PATH:
   - Right-click "This PC" → Properties → Advanced system settings → Environment Variables
   - Find `Path` in "System variables", edit it, and add `C:\msys64\mingw64\bin`
   - Restart your terminal for the PATH to take effect

Verify:

```powershell
gcc --version
```

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
$env:AGENT_DIRS = "C:\Users\YourName\.chrys\sessions;C:\Users\YourName\.claude\projects"
.\session-insight.exe
```

## Troubleshooting

**`gcc: not found` or `exec: "gcc": executable file not found`**

The mingw64 gcc is not in your PATH. Confirm that `C:\msys64\mingw64\bin` has been added to your system PATH and restart your terminal.

**`sqlite3-binding.c: fatal error C1083: Cannot open include file: 'stdio.h'`**

This indicates you're using MSVC instead of mingw-w64's gcc. Ensure the mingw64 path comes before MSVC in your PATH, or run the build commands in the MSYS2 MinGW 64-bit terminal.

**`npm run build` fails**

Try deleting `frontend/node_modules` and running `npm ci` again.
