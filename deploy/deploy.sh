#!/usr/bin/env bash
# Build for the Pi and stage a new amythest binary + unit on the host.
# Finalizing the swap (mv + restart) is manual — see deploy/DEPLOY.md.
set -euo pipefail
HOST="${1:?usage: deploy.sh user@host}"

make assets
GOOS=linux GOARCH=arm64 go build -o bin/amythest-linux-arm64 ./cmd/amythest

ssh "$HOST" 'mkdir -p ~/bin ~/.config/amythest'
scp bin/amythest-linux-arm64 "$HOST":~/bin/amythest.new
scp deploy/amythest.service "$HOST":~/.config/systemd/user/amythest.service 2>/dev/null || {
  ssh "$HOST" 'mkdir -p ~/.config/systemd/user'
  scp deploy/amythest.service "$HOST":~/.config/systemd/user/amythest.service
}
ssh "$HOST" 'test -f ~/.config/amythest/env || echo "NOTE: create ~/.config/amythest/env (see deploy/amythest.env.example)"'

echo "Staged. On the host:"
echo "  mv ~/bin/amythest.new ~/bin/amythest"
echo "  systemctl --user daemon-reload && systemctl --user restart amythest"
