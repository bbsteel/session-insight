#!/usr/bin/env bash
# Shorthand for the terminal Reference Manager: ./terminal-reference.sh [agent]
# Equivalent to ./scripts/terminal-reference; see
# developer/agent-adapters/reference-manager/README.md for details.
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scripts/terminal-reference" "$@"
