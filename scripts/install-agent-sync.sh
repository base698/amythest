#!/usr/bin/env bash
# Import an agent account (e.g. Slack) on a schedule, so replies arrive
# without running sync by hand. Each run reads only recent messages
# (--recent 2 days, --limit 30): every run is a Claude call, so keep it small.
#
#   scripts/install-agent-sync.sh NAME [MINUTES]   install or update (default 5)
#   scripts/install-agent-sync.sh NAME uninstall   stop and remove
set -euo pipefail
name="${1:?Usage: install-agent-sync.sh NAME [MINUTES|uninstall]}"
case "$name" in *[!A-Za-z0-9._-]*) echo "Invalid account name" >&2; exit 2;; esac
label="dev.amythest.agent-sync.$name"
plist="$HOME/Library/LaunchAgents/$label.plist"
domain="gui/$(id -u)"
if [[ "${2:-}" == uninstall ]]; then
  launchctl bootout "$domain/$label" 2>/dev/null || true
  rm -f "$plist"
  echo "Removed $label."
  exit 0
fi
minutes="${2:-5}"
[[ "$minutes" =~ ^[0-9]+$ && "$minutes" -ge 1 ]] || { echo "MINUTES must be a positive whole number" >&2; exit 2; }
connector="$HOME/.local/share/amythest-agent-messages/connector.py"
[[ -f "$connector" ]] || { echo "Install the agent connector first: scripts/install-agent-messages.sh" >&2; exit 1; }
python="$(command -v python3)"
claude_dir="$(dirname "$(command -v claude)")"
settings="${AMYTHEST_MESSAGES_CONFIG:-$HOME/.config/amythest/messages.json}"
logs="$HOME/.local/state/amythest-agent-messages/$name"
install -d -m 0700 "$logs"
mkdir -p "$HOME/Library/LaunchAgents"
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array>
    <string>$python</string><string>$connector</string>
    <string>--settings</string><string>$settings</string>
    <string>sync</string><string>--name</string><string>$name</string>
    <string>--recent</string><string>2</string><string>--limit</string><string>30</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>$claude_dir:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>StartInterval</key><integer>$((minutes * 60))</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>WorkingDirectory</key><string>$HOME</string>
  <key>StandardOutPath</key><string>$logs/sync.log</string>
  <key>StandardErrorPath</key><string>$logs/sync-error.log</string>
</dict></plist>
PLIST
launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "Importing $name every $minutes minutes. Logs: $logs/sync.log"
