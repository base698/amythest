#!/usr/bin/env bash
# Open the amy board beside the current work, or focus the one already open.
# Opening a second board would be the wrong answer: the running one holds
# your column, cursor and any in-flight edit.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
herdr_bin="${HERDR_BIN_PATH:-herdr}"

if found=$("$root/scripts/amy.sh" herdr find-pane --label amy 2>/dev/null); then
  tab="${found%% *}"
  pane="${found##* }"
  "$herdr_bin" tab focus "$tab" >/dev/null
  "$herdr_bin" plugin pane focus "$pane" >/dev/null
  exit 0
fi

open_args=(plugin pane open --plugin amythest --entrypoint board --direction right)
if [[ -n "${HERDR_PANE_ID:-}" ]]; then
  open_args+=(--target-pane "$HERDR_PANE_ID")
fi
"$herdr_bin" "${open_args[@]}" >/dev/null
