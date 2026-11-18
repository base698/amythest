#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install -d -m 0700 "$HOME/.local/share/amythest-mail"
install -d "$HOME/bin"
install -m 0600 "$repo_dir/integrations/mail/connector.py" "$repo_dir/integrations/mail/oauth.py" \
  "$repo_dir/integrations/mail/providers.py" "$repo_dir/integrations/common/messaging_common.py" \
  "$HOME/.local/share/amythest-mail/"
install -m 0755 "$repo_dir/integrations/mail/amy-mail" "$HOME/bin/amy-mail"
echo 'Installed amy-mail. OAuth credentials and account settings stay outside this checkout.'
