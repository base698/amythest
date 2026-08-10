#!/usr/bin/env bash
# Kanban wire-compatibility gate: boots amythest on a temp vault (optionally
# seeded from $REAL_BOARDS) and drives the bundled kanban.py skill client
# through the full card lifecycle. Any non-zero exit means the API drifted
# from the kanban wire contract.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KANBAN_PY="${KANBAN_PY:-$REPO_ROOT/.claude/skills/kanban/scripts/kanban.py}"
if [[ ! -f "$KANBAN_PY" ]]; then
  echo "SKIP: kanban.py not found at $KANBAN_PY"
  exit 0
fi

TMP=$(mktemp -d)
trap 'kill $SRV_PID 2>/dev/null || true; rm -rf "$TMP"' EXIT

mkdir -p "$TMP/vault/kanban"
if [[ -n "${REAL_BOARDS:-}" && -d "${REAL_BOARDS:-}" ]]; then
  cp -RL "$REAL_BOARDS/" "$TMP/vault/kanban/" 2>/dev/null || true
  rm -f "$TMP"/vault/kanban/*/.lock 2>/dev/null || true
fi
echo "# compat vault" > "$TMP/vault/index.md"

export KANBAN_BASE_URL="http://127.0.0.1:8641/kanban"
export KANBAN_USERNAME=compat
export KANBAN_PASSWORD='compat-password-1'
export KANBAN_SESSION_SECRET='0123456789abcdef0123456789abcdef'
export KANBAN_SESSION_FILE="$TMP/session.json"
export KANBAN_ENV_FILE="$TMP/no-env-file"

./bin/amythest -vault "$TMP/vault" -data "$TMP/data" -listen 127.0.0.1:8641 \
  > "$TMP/server.log" 2>&1 &
SRV_PID=$!

for i in $(seq 1 50); do
  curl -sf http://127.0.0.1:8641/health >/dev/null 2>&1 && break
  sleep 0.2
done

k() { python3 "$KANBAN_PY" "$@"; }

echo "== amy client smoke =="
go build -o "$TMP/amy" ./cmd/amy
"$TMP/amy" -check -endpoint http://127.0.0.1:8641

echo "== boards list =="
k boards | tee "$TMP/boards.txt"

echo "== create board =="
k new-board compat-suite
k boards | grep -q compat-suite

echo "== create card =="
CARD_JSON=$(k create compat-suite --title "Compat lifecycle card" --description "created by compat.sh" --status triage --label compat)
CARD_ID=$(echo "$CARD_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
echo "card: $CARD_ID"

echo "== update + comment =="
k update compat-suite "$CARD_ID" --assignee compat-user --labels compat,suite
k comment compat-suite "$CARD_ID" --body "compat comment body"
k card compat-suite "$CARD_ID" | python3 -c '
import json,sys
c=json.load(sys.stdin)
assert c["assignee"]=="compat-user", c
assert "suite" in c["labels"], c
assert any("compat comment body"==x["body"] for x in c["comments"]), c
print("update+comment ok")'

echo "== move through columns =="
k move compat-suite "$CARD_ID" --status backlog
k move compat-suite "$CARD_ID" --status ready
k move compat-suite "$CARD_ID" --status in_progress
k move compat-suite "$CARD_ID" --status verify

echo "== attach =="
echo "attachment payload" > "$TMP/file.txt"
k attach compat-suite "$CARD_ID" "$TMP/file.txt"
k attachments compat-suite "$CARD_ID" | grep -q file.txt

echo "== archive (move done) + restore =="
k move compat-suite "$CARD_ID" --status done
k archive compat-suite | grep -q "$CARD_ID"
k restore compat-suite "$CARD_ID" --status verify
k card compat-suite "$CARD_ID" | python3 -c '
import json,sys
c=json.load(sys.stdin)
assert c["status"]=="verify", c
print("restore ok")'

echo "== search + delete =="
k search --board compat-suite --json "lifecycle" | grep -q "$CARD_ID"
k delete compat-suite "$CARD_ID"
if k card compat-suite "$CARD_ID" 2>/dev/null | grep -q '"id"'; then
  echo "FAIL: card still present after delete"; exit 1
fi

echo "== board.md format sanity =="
grep -q 'AMYTHEST_KANBAN_DATA_START' "$TMP/vault/kanban/compat-suite/board.md"

echo "COMPAT SUITE PASSED"
