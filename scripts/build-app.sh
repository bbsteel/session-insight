#!/usr/bin/env bash
# Build the standalone desktop app variant: same server and embedded frontend,
# but the UI opens in a native webview window (WebKitGTK on Linux, WebKit on
# macOS, WebView2 on Windows) instead of a browser tab.
#
# Usage: scripts/build-app.sh [output-path]
#   output-path defaults to dist/session-insight-app
#
# Prerequisites:
#   - Go 1.25+, Node.js 18+ (frontend/dist must be built)
#   - A CGO C/C++ toolchain
#   - Linux only: GTK3 + WebKitGTK dev files
#       Arch:          sudo pacman -S webkit2gtk-4.1
#       Debian/Ubuntu: sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
#
# Immutable-OS hosts (e.g. SteamOS) strip C library/GTK dev files from /usr
# and package installs need root. Set SI_APP_SYSROOT to a user-space sysroot
# containing those dev files; pkg-config lookups and CGO compiles are
# redirected into it while linking stays dynamic against the host runtime.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

output=${1:-"dist/session-insight-app"}
alias_dir="${repo_root}/scripts/pkgconfig"

if [ ! -d frontend/dist ]; then
	echo "[build-app] error: frontend/dist is missing; run 'npm --prefix frontend run build' first" >&2
	exit 1
fi

# Immutable-OS support: redirect pkg-config and CGO into the sysroot first so
# the webkit checks below (and the 4.0→4.1 alias) see the sysroot contents.
if [ -n "${SI_APP_SYSROOT:-}" ]; then
	sysroot=$(cd "${SI_APP_SYSROOT}" && pwd)
	export PKG_CONFIG_SYSROOT_DIR="${sysroot}"
	export PKG_CONFIG_PATH="${sysroot}/usr/lib/pkgconfig:${sysroot}/usr/share/pkgconfig${PKG_CONFIG_PATH:+:${PKG_CONFIG_PATH}}"
	for var in CGO_CFLAGS CGO_CXXFLAGS CGO_LDFLAGS; do
		export "${var}=""--sysroot=${sysroot} ${!var:-}"""
	done
	echo "[build-app] using sysroot at ${sysroot} (SI_APP_SYSROOT)"
fi

# webview_go asks pkg-config for webkit2gtk-4.0, which current distros have
# replaced with 4.1. Forward the lookup through the alias .pc in
# scripts/pkgconfig when only 4.1 is installed.
if [ "$(uname -s)" = "Linux" ]; then
	if ! command -v pkg-config >/dev/null 2>&1; then
		echo "[build-app] error: pkg-config is required on Linux (install pkgconf/pkg-config)" >&2
		exit 1
	fi
	if ! pkg-config --exists webkit2gtk-4.0; then
		if pkg-config --exists webkit2gtk-4.1; then
			export PKG_CONFIG_PATH="${alias_dir}${PKG_CONFIG_PATH:+:${PKG_CONFIG_PATH}}"
			echo "[build-app] webkit2gtk-4.0 not found; using ${alias_dir}/webkit2gtk-4.0.pc alias to 4.1"
		else
			echo "[build-app] error: neither webkit2gtk-4.0 nor webkit2gtk-4.1 found" >&2
			echo "  Arch:          sudo pacman -S webkit2gtk-4.1" >&2
			echo "  Debian/Ubuntu: sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev" >&2
			exit 1
		fi
	fi
fi

version=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
commit=$(git rev-parse --short HEAD 2>/dev/null || echo "")

mkdir -p "$(dirname "$output")"

CGO_ENABLED=1 go build -trimpath \
	-tags "webview sqlite_fts5" \
	-ldflags "-X main.version=${version} -X main.commit=${commit}" \
	-o "$output" .

echo "[build-app] built ${output} (version=${version} commit=${commit})"
