#!/usr/bin/env bash
# Deploy the optional gateway and messaging connectors to a configured Linux host.
# Connector units remain stopped on first install, pending interactive login.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote_host="${1:?Usage: deploy-messages.sh SSH_HOST [arm64|amd64]}"
case "$remote_host" in -*|*[[:space:]]*) echo "Invalid SSH host" >&2; exit 2;; esac
remote_arch="${2:-arm64}"
case "$remote_arch" in arm64|amd64) ;; *) echo "Expected arm64 or amd64" >&2; exit 2;; esac
staging_dir="$(mktemp -d)"
trap 'rm -rf "$staging_dir"' EXIT
cd "$repo_dir"
GOOS=linux GOARCH="$remote_arch" go build -ldflags='-s -w' -o bin/amy-linux ./cmd/amy
tar_opts=()
if [[ "$(uname -s)" == Darwin ]]; then tar_opts+=(--no-xattrs); fi
COPYFILE_DISABLE=1 tar "${tar_opts[@]}" -czf "$staging_dir/bundle.tgz" bin/amy-linux \
  integrations/telegram/connector.py integrations/telegram/requirements.txt \
  integrations/telegram/amy-telegram integrations/telegram/amy-messages \
  integrations/common/messaging_common.py integrations/signal/connector.py integrations/signal/amy-signal \
  scripts/install-signal-runtime.sh \
  deploy/amythest-messages.service deploy/amythest-telegram.service deploy/amythest-signal.service
ssh_opts=(-o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=5 -o ServerAliveCountMax=3)
ssh "${ssh_opts[@]}" "$remote_host" 'install -d -m 0700 ~/.local/share/amythest-messaging; cat > ~/.local/share/amythest-messaging/deploy.tgz' < "$staging_dir/bundle.tgz"
ssh "${ssh_opts[@]}" "$remote_host" 'bash -se' <<'REMOTE'
set -euo pipefail
install -d -m 0700 ~/.local/share/amythest-messaging/deploy ~/.local/share/amythest-telegram ~/.local/share/amythest-signal
install -d ~/bin ~/.config/systemd/user
tar -xzf ~/.local/share/amythest-messaging/deploy.tgz -C ~/.local/share/amythest-messaging/deploy
cd ~/.local/share/amythest-messaging/deploy
python3 -m venv ~/.local/share/amythest-telegram/venv
~/.local/share/amythest-telegram/venv/bin/pip install -r integrations/telegram/requirements.txt
install -m 0600 integrations/telegram/connector.py integrations/telegram/requirements.txt ~/.local/share/amythest-telegram/
install -m 0600 integrations/common/messaging_common.py ~/.local/share/amythest-telegram/
install -m 0600 integrations/common/messaging_common.py integrations/signal/connector.py ~/.local/share/amythest-signal/
install -m 0700 scripts/install-signal-runtime.sh ~/.local/share/amythest-signal/
install -m 0755 integrations/signal/amy-signal ~/bin/
install -m 0755 integrations/telegram/amy-telegram integrations/telegram/amy-messages ~/bin/
install -m 0755 bin/amy-linux ~/.local/share/amythest-messaging/amy.next
mv ~/.local/share/amythest-messaging/amy.next ~/.local/share/amythest-messaging/amy
install -m 0644 deploy/amythest-messages.service deploy/amythest-telegram.service deploy/amythest-signal.service ~/.config/systemd/user/
active_connectors=()
for connector in telegram signal; do
  if systemctl --user is-active --quiet "amythest-$connector.service"; then
    active_connectors+=("amythest-$connector.service")
  fi
done
systemctl --user daemon-reload
systemctl --user enable amythest-messages.service
systemctl --user restart amythest-messages.service
for check in 1 2 3 4 5; do
  if ~/bin/amy-messages connectors >/dev/null 2>&1; then break; fi
  sleep 1
done
~/bin/amy-messages connectors >/dev/null
for service in "${active_connectors[@]}"; do
  systemctl --user restart "$service"
done
systemctl --user is-active amythest-messages.service
REMOTE
