# Source this file before `go build` / `go test` of this repo on Linux
# (run.sh, scripts/start.sh, and CI do this automatically).
#
# The unified binary keeps GTK/WebKit symbols weak + undefined at link time
# (webview_stubs_linux.S) and loads WebKitGTK on demand when `--app` is used.
# Building requires the wrapper in this directory as PKG_CONFIG: it forwards
# webkit2gtk-4.0 to 4.1 and strips webview link flags so the stack is not
# recorded as a startup dependency. Compiling webview.cc still needs the
# GTK/WebKit *headers*; install the dev packages (Arch: webkit2gtk-4.1;
# Debian/Ubuntu: libgtk-3-dev libwebkit2gtk-4.1-dev) or point SI_APP_SYSROOT
# at a sysroot containing them.
#
# This file only takes effect on Linux.

if [ "$(uname -s)" = "Linux" ]; then
	buildtools_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	repo_root="$(cd "${buildtools_dir}/../.." && pwd)"
	export PKG_CONFIG="${buildtools_dir}/pkg-config"

	# The pkg-config wrapper re-emits the default include root as -idirafter
	# (so libc++ keeps its header priority under alternate C++ toolchains like
	# zig c++); cgo rejects that flag by default — allow it for this build env.
	case " ${CGO_CFLAGS_ALLOW:-} " in
	*"-idirafter"*) ;;
	*) export CGO_CFLAGS_ALLOW="${CGO_CFLAGS_ALLOW:+$CGO_CFLAGS_ALLOW }-idirafter.*" ;;
	esac

	# Immutable-OS hosts (e.g. SteamOS) strip C library/GTK dev files from
	# /usr; a user-space sysroot supplying headers + .pc files can be provided
	# without root. Linking stays dynamic against host runtime libraries.
	# Multiarch layouts (Debian/Ubuntu usr/lib/<triplet>/pkgconfig, Arch
	# usr/lib/pkgconfig) are all included without consulting host metadata.
	if [ -n "${SI_APP_SYSROOT:-}" ]; then
		sysroot="$(cd "${SI_APP_SYSROOT}" && pwd)"
		export PKG_CONFIG_SYSROOT_DIR="${sysroot}"
		sysroot_pc="${sysroot}/usr/lib/pkgconfig:${sysroot}/usr/share/pkgconfig"
		for arch_pc_dir in "${sysroot}"/usr/lib/*-linux-gnu/pkgconfig; do
			if [ -d "${arch_pc_dir}" ]; then
				sysroot_pc="${sysroot_pc}:${arch_pc_dir}"
			fi
		done
		for arch_pc_dir in "${sysroot}"/usr/lib/pkgconfig/*/; do
			if [ -d "${arch_pc_dir}" ]; then
				sysroot_pc="${sysroot_pc}:${arch_pc_dir%/}"
			fi
		done
		export PKG_CONFIG_PATH="${sysroot_pc}${PKG_CONFIG_PATH:+:${PKG_CONFIG_PATH}}"
		export CGO_CFLAGS="--sysroot=${sysroot} ${CGO_CFLAGS:-}"
		export CGO_CXXFLAGS="--sysroot=${sysroot} ${CGO_CXXFLAGS:-}"
		export CGO_LDFLAGS="--sysroot=${sysroot} ${CGO_LDFLAGS:-}"
	fi

	# The weak-symbol header is force-included into every cgo C/C++ compile so
	# the webview backend's GTK/WebKit references are emitted weak (PLT entries
	# instead of hard link requirements); appwindow_linux.go dlopen()s the real
	# library when `--app` is used. See weak-gtk-symbols.h for the full story.
	weak_header="${buildtools_dir}/weak-gtk-symbols.h"
	if [ -f "${weak_header}" ]; then
		export CGO_CFLAGS="-include ${weak_header} ${CGO_CFLAGS:-}"
		export CGO_CXXFLAGS="-include ${weak_header} ${CGO_CXXFLAGS:-}"
	fi
fi
