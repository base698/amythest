#!/usr/bin/env bash
# Open the wall beside the current pane, or close the one this workspace
# already shows. One wall per workspace: each shows that workspace's project,
# so a wall open elsewhere is left alone.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
herdr_bin="${HERDR_BIN_PATH:-herdr}"

if [[ -n "${HERDR_WORKSPACE_ID:-}" ]] &&
  found=$("$root/scripts/amy.sh" herdr find-pane --label wall --workspace "$HERDR_WORKSPACE_ID" 2>/dev/null); then
  "$herdr_bin" plugin pane close "${found##* }" >/dev/null
  exit 0
fi

open_args=(plugin pane open --plugin amythest --entrypoint wall --direction right --no-focus)
if [[ -n "${HERDR_PANE_ID:-}" ]]; then
  open_args+=(--target-pane "$HERDR_PANE_ID")
fi
"$herdr_bin" "${open_args[@]}" >/dev/null
