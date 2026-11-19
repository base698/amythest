#!/usr/bin/env bash
# Run the iMessage connector in the background at login, from a small signed
# app that holds its macOS permissions (Full Disk Access to read Messages,
# Automation to send). Grant both to "Amythest iMessage" once; connector
# updates (install-imessage-connector.sh) do not need them granted again.
#
#   scripts/install-imessage-service.sh            install or update, then start
#   scripts/install-imessage-service.sh uninstall  stop and remove the agent
#
# The app is rebuilt only when the launcher's source changes, because macOS
# ties permissions to the exact signed program.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
label=dev.amythest.imessage
app="$HOME/Applications/Amythest iMessage.app"
plist="$HOME/Library/LaunchAgents/$label.plist"
state="$HOME/.local/state/amythest-imessage"
domain="gui/$(id -u)"

if [[ "${1:-}" == uninstall ]]; then
  launchctl bootout "$domain/$label" 2>/dev/null || true
  rm -f "$plist"
  echo "Stopped and removed $label. The app stays in ~/Applications; delete it to revoke its permissions."
  exit 0
fi

python="$(command -v python3)"
[[ -x "$python" ]] || { echo "python3 not found" >&2; exit 1; }
connector="$HOME/.local/share/amythest-imessage/connector.py"
[[ -f "$connector" ]] || bash "$repo_dir/scripts/install-imessage-connector.sh"

src="$repo_dir/integrations/imessage/launcher/main.go"
want="$(shasum -a 256 "$src" | cut -d' ' -f1)"
have="$(cat "$app/Contents/Resources/launcher.sha256" 2>/dev/null || true)"
if [[ "$want" != "$have" ]]; then
  [[ -z "$have" ]] || echo "The launcher changed: grant its permissions again after this."
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  mkdir -p "$tmp/app/Contents/MacOS" "$tmp/app/Contents/Resources"
  (cd "$repo_dir" && go build -trimpath -o "$tmp/app/Contents/MacOS/amythest-imessage" ./integrations/imessage/launcher)
  cat > "$tmp/app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>$label</string>
  <key>CFBundleName</key><string>Amythest iMessage</string>
  <key>CFBundleExecutable</key><string>amythest-imessage</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSUIElement</key><true/>
  <key>NSAppleEventsUsageDescription</key><string>Amythest sends the replies you queue through Messages.</string>
</dict></plist>
PLIST
  echo "$want" > "$tmp/app/Contents/Resources/launcher.sha256"
  codesign --force --sign - --identifier "$label" "$tmp/app"
  mkdir -p "$HOME/Applications"
  launchctl bootout "$domain/$label" 2>/dev/null || true
  rm -rf "$app"
  mv "$tmp/app" "$app"
fi

install -d -m 0700 "$state"
mkdir -p "$HOME/Library/LaunchAgents"
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array>
    <string>$app/Contents/MacOS/amythest-imessage</string>
    <string>$python</string>
    <string>$connector</string>
    <string>service</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>ProcessType</key><string>Background</string>
  <key>WorkingDirectory</key><string>$HOME</string>
  <key>StandardOutPath</key><string>$state/service.log</string>
  <key>StandardErrorPath</key><string>$state/service-error.log</string>
</dict></plist>
PLIST
launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "Started $label. Logs: $state/service-error.log"
echo "First time: System Settings → Privacy & Security → Full Disk Access → + → ~/Applications/Amythest iMessage.app,"
echo "then: launchctl kickstart -k $domain/$label"
