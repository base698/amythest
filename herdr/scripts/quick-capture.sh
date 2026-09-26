#!/usr/bin/env bash
# Pop the capture prompt over whatever is on screen. The pane is declared
# `popup` in the manifest, so herdr owns the modality and closes it when
# amy exits.
set -euo pipefail
herdr_bin="${HERDR_BIN_PATH:-herdr}"
"$herdr_bin" plugin pane open --plugin amythest --entrypoint capture >/dev/null
