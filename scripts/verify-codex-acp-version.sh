#!/usr/bin/env bash
set -euo pipefail

readonly registry_url="https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"
readonly source_file="internal/llm/acp.go"

pinned_package="$({
  sed -n 's/^const codexACPAgentPackage = "\([^"]*\)"$/\1/p' "${source_file}"
} | head -n 1)"
if [[ -z "${pinned_package}" ]]; then
  echo "Could not read codexACPAgentPackage from ${source_file}" >&2
  exit 1
fi

registry_package="$({
  curl --fail --silent --show-error --location "${registry_url}"
} | jq --exit-status --raw-output '.agents[] | select(.id == "codex-acp") | .distribution.npx.package')"

if [[ "${pinned_package}" != "${registry_package}" ]]; then
  echo "Codex ACP adapter is stale: pinned=${pinned_package} registry=${registry_package}" >&2
  echo "Update codexACPAgentPackage, exercise model discovery, and commit the tested version before releasing." >&2
  exit 1
fi

echo "Codex ACP adapter matches the registry: ${pinned_package}"
