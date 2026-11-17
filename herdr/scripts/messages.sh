#!/usr/bin/env bash
set -euo pipefail
herdr_bin="${HERDR_BIN_PATH:-herdr}"
"$herdr_bin" plugin pane open --plugin amythest --entrypoint messages >/dev/null
