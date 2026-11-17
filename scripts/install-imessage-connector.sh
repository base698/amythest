#!/usr/bin/env bash
# Installation only. Settings and the connector token stay outside this checkout.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install -d -m 0700 "$HOME/.local/share/amythest-imessage"
install -d "$HOME/bin"
install -m 0600 "$repo_dir/integrations/imessage/connector.py" \
  "$repo_dir/integrations/common/messaging_common.py" "$repo_dir/integrations/common/stdio_rpc.py" \
  "$HOME/.local/share/amythest-imessage/"
install -m 0755 "$repo_dir/integrations/imessage/amy-imessage" "$HOME/bin/amy-imessage"
echo 'Installed amy-imessage. Configure private settings, then run amy-imessage doctor.'
