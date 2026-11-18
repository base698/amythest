#!/usr/bin/env bash
# Set up Amythest on a Mac: build it, run the server as a login service, and hook up
# amy, the Herdr App shortcuts and the Work collector. Safe to re-run: existing
# config, credentials and secrets are kept; binaries and services are refreshed.
#
#   scripts/setup-mac.sh                          everything, with a local server
#   scripts/setup-mac.sh --vault ~/work-notes     vault folder (default ~/notes)
#   scripts/setup-mac.sh --port 8639              local server port (default 8639)
#   scripts/setup-mac.sh --remote https://host/notes
#                                                 use an existing server instead of a local one
#   scripts/setup-mac.sh --skip herdr,collector   leave out parts: build,server,cli,herdr,collector
#   scripts/setup-mac.sh --dry-run                show what would happen
#
# Files: ~/bin/{amythest,amy}, ~/.config/amythest/{amythest.yaml,env,cli.yaml},
# ~/.local/state/amythest (index), ~/Library/LaunchAgents/dev.amythest.server.plist,
# ~/Library/Logs/amythest.log. The collector installs via deploy/install-collector.py.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
vault="$HOME/notes" port=8639 remote="" skip="" dry=0
while (( $# )); do
  case "$1" in
    --vault) vault="${2/#\~/$HOME}"; shift ;;
    --port) port="$2"; shift ;;
    --remote) remote="${2%/}"; shift ;;
    --skip) skip="$2"; shift ;;
    --dry-run|-n) dry=1 ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done

cfg="$HOME/.config/amythest"
state="$HOME/.local/state/amythest"
bin="$HOME/bin"
label="dev.amythest.server"
plist="$HOME/Library/LaunchAgents/$label.plist"
log="$HOME/Library/Logs/amythest.log"
local_server=$([[ -z $remote ]] && echo 1 || echo 0)
server_url="${remote:-http://127.0.0.1:$port/notes}"

want() { [[ ",$skip," != *",$1,"* ]]; }
step() { printf '\n== %s\n' "$*"; }
run() { if (( dry )); then echo "  would: $*"; else "$@"; fi; }
need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1 ($2)" >&2; exit 1; }; }
# write <path> <mode>: stdin to a new file, only if it doesn't exist yet.
write() {
  if [[ -e "$1" ]]; then echo "  kept    ${1/#$HOME/~}"; cat >/dev/null; return; fi
  if (( dry )); then echo "  would create ${1/#$HOME/~}"; cat >/dev/null; return; fi
  mkdir -p "$(dirname "$1")"; (umask 077; cat >"$1"); chmod "$2" "$1"
  echo "  created ${1/#$HOME/~}"
}

need go "brew install go"
need python3 "xcode-select --install"

if want build; then
  step "build"
  targets=(build-amy build-collector)
  if (( local_server )); then need npm "brew install node"; targets=(build "${targets[@]}"); fi
  run make -C "$root" "${targets[@]}"
  run mkdir -p "$bin"
  run install -m 0755 "$root/bin/amy" "$bin/amy"
  (( local_server )) && run install -m 0755 "$root/bin/amythest" "$bin/amythest"
  echo "  installed ~/bin/amy$( (( local_server )) && echo ' ~/bin/amythest')"
  [[ ":$PATH:" == *":$bin:"* ]] || echo "  note: add ~/bin to PATH"
fi

if want server && (( local_server )); then
  step "server ($server_url, vault ${vault/#$HOME/~})"
  run mkdir -p "$vault" "$state"
  write "$cfg/amythest.yaml" 644 <<EOF
# Amythest server on this Mac. See amythest.example.yaml and docs/authentication.md.
vault: $vault
listen: 127.0.0.1:$port
base_url: /notes
data_dir: $state
site_name: Amythest
rescan_interval: 5m

auth_mode: private
auth_public_url: $server_url
# Loopback-only plain HTTP. Remove if you put TLS (Tailscale Serve, a proxy) in front.
auth_allow_http: true
auth_session_ttl: 2160h

work:
  enabled: true
EOF
  # Login for the web UI and amy; KANBAN_* names are read by both server and amy.
  write "$cfg/env" 600 <<EOF
KANBAN_USERNAME=$USER
KANBAN_PASSWORD=$(openssl rand -base64 18 | tr -d '/+=')
KANBAN_SESSION_SECRET=$(openssl rand -hex 32)
KANBAN_COOKIE_SECURE=false
EOF
  run mkdir -p "$(dirname "$plist")" "$(dirname "$log")"
  if (( dry )); then echo "  would write ${plist/#$HOME/~} and (re)start $label"
  else
    # The env file is sourced at start so secrets stay out of the plist.
    cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string><string>-c</string>
    <string>set -a; . "$cfg/env"; set +a; exec "$bin/amythest" -config "$cfg/amythest.yaml"</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict><key>GOMEMLIMIT</key><string>300MiB</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>$log</string>
  <key>StandardErrorPath</key><string>$log</string>
</dict>
</plist>
EOF
    plutil -lint -s "$plist"
    launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
    for _ in $(seq 50); do launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1 || break; sleep 0.1; done
    launchctl bootstrap "gui/$(id -u)" "$plist"
    printf '  waiting for the server'
    code=000
    for _ in $(seq 60); do
      code=$(curl -s -o /dev/null -w '%{http_code}' "$server_url/" || true)
      [[ $code =~ ^(200|30[0-9])$ ]] && break
      printf .; sleep 1
    done
    echo
    if [[ $code =~ ^(200|30[0-9])$ ]]; then echo "  up: $server_url  (login: $USER, password in ~/.config/amythest/env)"
    else echo "  server did not answer ($code); see ${log/#$HOME/~}" >&2; exit 1; fi
  fi
fi

if want cli; then
  step "amy"
  write "$cfg/cli.yaml" 644 <<EOF
endpoint: $server_url
EOF
  if (( ! local_server )) && [[ ! -e "$cfg/env" ]]; then
    echo "  amy needs KANBAN_USERNAME / KANBAN_PASSWORD for $remote in ~/.config/amythest/env (mode 600)"
  fi
fi

if want herdr; then
  step "Herdr"
  if [[ -d "${HERDR_APP_HOME:-$HOME/.config/herdr-app}" ]]; then
    run python3 "$root/scripts/install-herdr-app-messaging.py" --binary "$bin/amy"
  else
    echo "  Herdr App not set up here (open it once, then re-run); skipping shortcuts"
  fi
  # Agent lifecycle events for the collector (status + Claude session ids).
  if command -v herdr >/dev/null 2>&1; then
    if herdr integration status 2>/dev/null | grep -q '^claude: current'; then echo "  claude integration: current"
    else run herdr integration install claude; fi
  fi
fi

if want collector; then
  step "collector"
  cred="${AMYTHEST_COLLECTOR_STATE:-$HOME/.local/state/amythest-collector}/credential.json"
  mode=()
  if [[ ! -e "$cred" ]]; then
    if [[ -t 0 ]] && (( ! dry )); then
      echo "  registering: approve in the browser (sign in as $USER; password in ~/.config/amythest/env)"
      if ! "$root/bin/amythest-collector" register --server "$server_url" --name "$(scutil --get LocalHostName 2>/dev/null || hostname -s)"; then
        echo "  registration didn't finish; installing in capture-only mode"; mode=(--capture-only)
      fi
    else
      echo "  no credential yet: run '$root/bin/amythest-collector register --server $server_url --name <this mac>'"
      echo "  then re-run this script; capturing locally until then"
      mode=(--capture-only)
    fi
  fi
  run python3 "$root/deploy/install-collector.py" --binary "$root/bin/amythest-collector" ${mode[@]+"${mode[@]}"}
fi

step "done"
echo "  web:  $server_url"
(( local_server )) && echo "  logs: ${log/#$HOME/~}   restart: launchctl kickstart -k gui/\$(id -u)/$label"
exit 0
