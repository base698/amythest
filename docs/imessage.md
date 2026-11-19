# Mac Messages feeder

The Mac reads its local Messages database and feeds the shared gateway over the
configured SSH tunnel. The gateway host holds the central
inbox, attention state, drafts and reply queue. Apple account/session data stays
on the Mac. The feeder's local state buffers ingress, send receipts and immutable outgoing file snapshots.

The adapter uses the installed [imsg](https://github.com/openclaw/imsg) CLI's
stdio JSON-RPC interface. It reads SQLite in read-only mode, decodes message text
through imsg, and submits sends through Messages.app automation. It does not
use injected private frameworks or change macOS security settings.

## Configure and run

Install the feeder files on the Mac:

```sh
bash scripts/install-imessage-connector.sh
```

Add `connectors.imessage` to the Mac's private messaging configuration:

```json
{
  "imessage": {
    "binary": "/opt/homebrew/bin/imsg",
    "amy_binary": "~/bin/amy",
    "database": "~/Library/Messages/chat.db",
    "state_dir": "~/.local/state/amythest-imessage",
    "account": "GENERATE-A-STABLE-DEVICE-UUID",
    "token_file": "~/.local/state/amythest-imessage/imessage.token",
    "all_chats": true,
    "chats": [],
    "initial": 10,
    "initial_days": 30,
    "services": ["iMessage", "SMS", "RCS"],
    "send": true
  }
}
```

Generate the device identifier with `uuidgen`, lowercase it, and keep it stable.
It namespaces native chat/message IDs without putting an Apple ID in settings.
Use your installation's binary paths. The gateway URL inherits `client.url`;
override it with `gateway` if needed. Copy the gateway's `imessage.token` over
SSH to the configured local private file (mode 0600). It cannot claim Signal or
Telegram deliveries or read the central inbox.

Grant Full Disk Access to the terminal/core hosting the feeder. Check access:

```sh
amy-imessage doctor
amy-imessage chats
amy-imessage service
```

`service` reconnects after errors; `watch` runs one session. A herdr-core
background terminal with `--outlive-parent` can host the feeder while the Mac
is available. It does not require an open Messages UI or focus pane. It is not
installed as an OS login service by this script.

For a narrower scope, set `all_chats: false` and copy selected native chat GUIDs
from `amy-imessage chats` into `chats`. Modern Messages databases may use `any;`
GUIDs even for iMessage chats; the feeder additionally checks `service_name` and
checks the configured services. It imports iMessage, SMS and RCS by default. Set `services` to a subset of
`["iMessage", "SMS", "RCS"]` to narrow that scope.

### Run in the background

`scripts/install-imessage-service.sh` runs the connector at login under
launchd, from a small signed app (`~/Applications/Amythest iMessage.app`) that
stays the connector's parent so macOS attributes its access to that app. Grant
it Full Disk Access once (System Settings → Privacy & Security → Full Disk
Access), stop any copy running in a terminal, then
`launchctl kickstart -k gui/$(id -u)/dev.amythest.imessage`. The first queued
send asks to let it control Messages. Logs are in
`~/.local/state/amythest-imessage/`. Connector updates keep the grant; the app
is rebuilt, and needs the grant again, only when its launcher changes.
`install-imessage-service.sh uninstall` stops and removes the agent.

## Behavior

- The first run considers the latest `initial` messages per selected chat
  within `initial_days` (default 30 days). New chats get the same bounded backfill.
  Later runs resume from a durable row cursor. Message GUIDs prevent duplicates.
- Incoming and outgoing messages update the conversation's latest activity.
  Outgoing messages appear in the thread without creating attention items.
  Reactions, service messages, retracted messages and expirable
  content are skipped. Received files that Messages has downloaded come with
  their message, read from `~/Library/Messages/Attachments` only. HEIC photos,
  and pictures over 5 MiB, are converted to JPEG (at most 2048 pixels) with
  macOS `sips`. Stickers, files not downloaded yet, and other files over 5 MiB
  point you back to Messages instead.
- Replies target the original native chat GUID. Installed imsg 0.4 supports
  conversation replies, not inline reply quotes. The composer explains this.
- One outbound file per reply, up to 5 MiB, uses the captured bytes and filename.
  The local snapshot remains available after handoff so Messages can finish
  consuming it asynchronously. macOS Automation access to Messages is needed for sends.
- A successful AppleScript handoff is **submitted**, not **delivered**: this API
  returns no native delivery receipt. Check Messages on the Mac. Submitted or
  uncertain attempts are not sent again automatically, and unconfirmed handoff
  does not clear the source's attention state. Press `d` to mark it handled.
- Replies are available while this Mac's feeder is connected. The central
  gateway retains the inbox when the Mac is offline. Explicitly submitted replies
  wait in its queue if the Mac worker was registered with sending enabled;
  disabled or unconfigured connectors leave replies as drafts.

Tests cover read-only access, native chat routing, exclusions, file snapshots,
permission rechecks and idempotent unconfirmed handoff. Automated sends use a
fake transport; actual sending needs Messages automation permission.
