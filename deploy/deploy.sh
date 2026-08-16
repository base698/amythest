#!/usr/bin/env bash
# Build for the Pi and stage a new amythest binary + unit on the host.
# Finalizing the swap (mv + restart) is manual — see deploy/DEPLOY.md.
set -euo pipefail
HOST="${1:?usage: deploy.sh user@host}"

make assets
GOOS=linux GOARCH=arm64 go build -o bin/amythest-linux-arm64 ./cmd/amythest

ssh "$HOST" 'mkdir -p ~/bin ~/.config/amythest ~/.config/systemd/user'
scp bin/amythest-linux-arm64 "$HOST":~/bin/amythest.new

# Never overwrite a live unit: hosts customize it (netexplore runs -config with
# listen :8088 and base_url /notes to match the portal proxy, while the unit in
# this repo hardcodes :8639 and no base URL). Clobbering it 502s the site.
if ssh "$HOST" 'test -f ~/.config/systemd/user/amythest.service'; then
  scp deploy/amythest.service "$HOST":~/.config/systemd/user/amythest.service.repo
  if ssh "$HOST" 'diff -q ~/.config/systemd/user/amythest.service ~/.config/systemd/user/amythest.service.repo >/dev/null'; then
    ssh "$HOST" 'rm -f ~/.config/systemd/user/amythest.service.repo'
  else
    echo "NOTE: host unit differs from deploy/amythest.service and was left in place."
    echo "      Repo copy staged at ~/.config/systemd/user/amythest.service.repo for comparison."
  fi
else
  scp deploy/amythest.service "$HOST":~/.config/systemd/user/amythest.service
fi

ssh "$HOST" 'test -f ~/.config/amythest/env || echo "NOTE: create ~/.config/amythest/env (see deploy/amythest.env.example)"'

echo "Staged. On the host:"
echo "  cp ~/bin/amythest ~/bin/amythest.rollback   # keep a way back"
echo "  mv ~/bin/amythest.new ~/bin/amythest && chmod +x ~/bin/amythest"
echo "  systemctl --user daemon-reload && systemctl --user restart amythest"
echo "  curl -s -o /dev/null -w '%{http_code}\\n' localhost:8088/health   # expect 200"
