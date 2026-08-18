#!/usr/bin/env bash
set -euo pipefail

readonly registry_url="https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"
readonly npm_registry_url="https://registry.npmjs.org"
readonly source_file="internal/llm/acp.go"

usage() {
  cat <<'EOF'
Usage: scripts/verify-codex-acp-version.sh [--latest]

The default mode verifies that the exact ACP package version pinned by the
adapter is still available from npm. The --latest mode additionally checks
whether the pin matches the current ACP registry entry and fails when an
upgrade is available.
EOF
}

check_latest_registry=false
case "${1:-}" in
  "")
    ;;
  --latest)
    check_latest_registry=true
    ;;
  --help|-h)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

if [[ "$#" -gt 1 ]]; then
  usage >&2
  exit 2
fi

pinned_package="$({
  sed -n 's/^const codexACPAgentPackage = "\([^"]*\)"$/\1/p' "${source_file}"
} | head -n 1)"
if [[ -z "${pinned_package}" ]]; then
  echo "Could not read codexACPAgentPackage from ${source_file}" >&2
  exit 1
fi

pinned_package_name="${pinned_package%@*}"
pinned_package_version="${pinned_package##*@}"
if [[ -z "${pinned_package_name}" || -z "${pinned_package_version}" || "${pinned_package_name}" == "${pinned_package}" ]]; then
  echo "Invalid codexACPAgentPackage value: ${pinned_package}" >&2
  exit 1
fi

encoded_package_name="$(jq -nr --arg package_name "${pinned_package_name}" '$package_name | @uri')"
npm_package_version="$({
  curl --fail --silent --show-error --location --retry 3 --retry-delay 1 \
    "${npm_registry_url}/${encoded_package_name}/${pinned_package_version}"
} | jq --exit-status --raw-output '.version // empty')"

if [[ "${npm_package_version}" != "${pinned_package_version}" ]]; then
  echo "Codex ACP adapter package is unavailable: pinned=${pinned_package}" >&2
  exit 1
fi

echo "Codex ACP adapter package is available: ${pinned_package}"

if [[ "${check_latest_registry}" != true ]]; then
  exit 0
fi

registry_package="$({
  curl --fail --silent --show-error --location --retry 3 --retry-delay 1 "${registry_url}"
} | jq --exit-status --raw-output '.agents[] | select(.id == "codex-acp") | .distribution.npx.package')"

if [[ "${pinned_package}" != "${registry_package}" ]]; then
  echo "Codex ACP adapter update available: pinned=${pinned_package} registry=${registry_package}" >&2
  echo "Update codexACPAgentPackage, exercise model discovery, and commit the tested version." >&2
  exit 1
fi

echo "Codex ACP adapter pin matches the registry: ${pinned_package}"
