# Personal Telegram connector

Amythest can receive personal Telegram cloud-chat messages and send replies to
their original chat, topic, and message. The connector runs separately from the
Go gateway, using Telethon 1.45.0. It works on macOS and Linux ARM64 without a
native TDLib dependency. Bot credentials from Hermes are not reused.

The code and gateway can be installed before an account is connected. A real
Telegram round trip requires your interactive login; fixture tests cannot verify
your account's delivery permissions.

## Private configuration

Hostnames, ports, chat selections and credential paths belong in
`~/.config/amythest/messages.json`, outside the checkout. See
[Messaging configuration](messages-config.md) for workstation and server examples.
The gateway, focus UI, wall sharing and connector read this configuration.
Command-line flags override it; `AMYTHEST_MESSAGES_CONFIG` selects another file.
The Telegram connector also accepts `--settings PATH`.

Install on a private Linux host with Python 3 and systemd user services:

```sh
scripts/deploy-messages.sh messaging-host arm64
```

Use `amd64` for an x86-64 host. Set the private server configuration first if the
default ports are occupied. Deployment preserves configuration, credentials and
state. It restarts an already-running connector; the first install leaves it
stopped pending login and chat selection.

Create application credentials at [Telegram API development tools](https://my.telegram.org/apps),
then use a terminal on the connector host:

```sh
~/bin/amy-telegram login
~/bin/amy-telegram chats
```

Login prompts for API ID/hash, phone number in international format, login code
and two-step password if needed. These stay on the connector host. Bot logins
are rejected. Set `connectors.telegram.chats` to the chosen numeric IDs as JSON
strings, and `send` to `true` to enable replies you explicitly queue. Use `me`
for a Saved Messages test. Start the service:

```sh
systemctl --user enable --now amythest-telegram.service
```

After changing scope, restart that service. Stop it before running `chats` or
`login`: one process owns the Telegram session at a time. `all_chats: true`
subscribes to every visible cloud chat; `send: false` receives without sending.
No automatic replies are generated.

## Focus, wall and Irssi

With your workstation configuration and private owner-token file in place:

```sh
amy messages
amy wall
```

The configured SSH tunnel opens automatically. There is no host-specific script.
In focus press `r` to reply, optionally attach a wall item, and `Ctrl+S` to send.
`D` shows delivery records. For Irssi configure the forwarded IRC port and owner
token file using [Messaging POC](messages-poc.md). The optional `irc_pane` setting
makes focus activate its containing herdr pane too.

For a local development connector:

```sh
python3 -m venv .venv-telegram
.venv-telegram/bin/pip install -r integrations/telegram/requirements.txt
.venv-telegram/bin/python integrations/telegram/connector.py login
.venv-telegram/bin/python integrations/telegram/connector.py watch
```

A deployed installation uses `~/.local/share/amythest-messaging/amy`,
`~/.local/share/amythest-telegram/`, wrappers in `~/bin`, and the generic user
units from `deploy/`. None of these deployment files contains personal settings.

The gateway's `telegram.token` is a separate connector credential. It permits
normalized ingress, heartbeat, claiming queued replies, scoped attachment reads,
and delivery receipts. It cannot read the whole inbox, create drafts, grant agent
permissions, or queue a send. Only the owner/authorized agent can queue replies.

## Delivery and recovery

A reply's immutable snapshot includes account ID, chat ID, topic ID, original
message ID, body, and file bytes. Display names cannot redirect it. The connector
checks that the original message still exists in the same chat/topic before
sending. Telegram text is literal; it is not parsed as Markdown or HTML.

Each queued reply is claimed once. Agent permissions are checked when queued,
when claimed, and again after attachment staging immediately before the provider
call. Scope changes or revoked grants block delivery. A successful receipt is
persisted locally before the connector reports it to the gateway. If the gateway
is down, the receipt is replayed without sending the Telegram message again.

A timeout, crash, or ambiguous provider error becomes **uncertain**. It is not
retried automatically, and `messages send` refuses to resend that draft. Check
the original Telegram conversation before creating a new reply. A known late
receipt can reconcile an uncertain record. Definite rejections become **failed**;
the owner can inspect them under `D` and explicitly retry. Source attention is
cleared only after a successful provider receipt.

Inbound messages are spooled in SQLite before posting to the gateway. A scan
checkpoint advances only after a completed history scan; live updates cannot
skip a backlog. On first import, the latest 30 messages per selected chat are
considered (`--initial` changes this up to 200). Subsequent scans recover messages
after the checkpoint. Replayed provider IDs do not reopen handled focus items.

Useful checks on the connector host:

```sh
~/bin/amy-messages connectors
systemctl --user status amythest-messages.service amythest-telegram.service
journalctl --user -u amythest-telegram.service --since '10 minutes ago'
```

## Current limits

- One attachment per live Telegram reply, up to 5 MiB. Images are sent as original
  documents so the bytes and filename are preserved. Text allows 4096 UTF-16 units;
  a file caption allows 1024. Demo replies still support eight attachments.
- Incoming photos and files up to 5 MiB are downloaded to the state directory's
  `incoming/` folder, delivered with their message, then removed. Larger files
  are named in the message and stay in Telegram. Message edits/deletions and read
  receipts are not synchronized yet.
  Native outgoing messages update recency and appear in the thread without
  creating attention items. Saved Messages is handled specially for
  testing, and this connector's own replies are excluded from its attention queue.
- Secret chats are not supported. Classification still uses local rules.
- The session file authorizes the personal account and stays in the connector's
  private directory. API grants are not an OS sandbox for an unrestricted agent.
- The initial unattended service only watches Saved Messages. Other chats need
  an explicit scope change. No real account or real delivery is implied by demo
  data or the automated tests.

## Verification

```sh
go test -race ./...
go vet ./...
AMYTHEST_TEST_BIN="$PWD/bin/amy" .venv-telegram/bin/python -m unittest discover -s integrations/telegram -v
```

Tests use a fake Telegram client around the actual connector. They exercise
routing, exact file bytes, literal text, loss of an HTTP receipt, uncertain
provider results, grant revocation, restart persistence, and ingestion checkpoints.
They do not send Telegram messages. Reference:
[Telethon client API](https://docs.telethon.dev/en/stable/modules/client.html),
[Telegram application credentials](https://core.telegram.org/api/obtaining_api_id).
