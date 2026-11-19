#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
install -d -m 0700 "$HOME/.local/share/amythest-agent-messages"
install -d "$HOME/bin"
install -m 0600 "$root/integrations/agent/connector.py" "$root/integrations/agent/chrome.py" "$root/integrations/agent/slack_worker.py" "$root/integrations/common/messaging_common.py" "$HOME/.local/share/amythest-agent-messages/"
install -m 0755 "$root/integrations/agent/amy-agent-messages" "$HOME/bin/amy-agent-messages"
echo 'Installed amy-agent-messages. Configure private connectors.agent.accounts before syncing.'
