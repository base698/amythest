#!/usr/bin/env bash
# Resolve the amy binary and exec it. Order matters: a built copy inside the
# plugin (what `plugin install` produces) wins, then a local dev install,
# then PATH. Keeps the manifest free of machine-specific paths.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for candidate in "$root/bin/amy" "$HOME/bin/amy"; do
  if [[ -x "$candidate" ]]; then exec "$candidate" "$@"; fi
done
if command -v amy >/dev/null 2>&1; then exec amy "$@"; fi

echo "amy binary not found — run 'go build -o bin/amy ./cmd/amy' in the amythest repo" >&2
# Hold the pane open so the error is readable instead of a pane that blinks shut.
read -r -p "press enter to close" _ || true
exit 127
