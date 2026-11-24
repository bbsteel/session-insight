# Source this file from the local build scripts. Release builds configure the
# desktop dependencies directly and continue to require them.
session_insight_build_tags=sqlite_fts5

if [[ "$(uname -s)" == "Linux" ]]; then
  buildtools_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -n "${SI_APP_SYSROOT:-}" ]] ||
    "$buildtools_dir/pkg-config" --exists gtk+-3.0 webkit2gtk-4.0 >/dev/null 2>&1; then
    # shellcheck source=scripts/buildtools/linux-cgo-env.sh
    source "$buildtools_dir/linux-cgo-env.sh"
  else
    echo "==> GTK/WebKitGTK development files unavailable; building for browser use"
    session_insight_build_tags+=,browseronly
  fi
fi
