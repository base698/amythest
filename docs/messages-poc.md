# Messaging proof of concept

This optional POC runs a separate private messaging gateway, a focus TUI, and a
small IRC interface for Irssi. It does not change the Amythest server. Demo mode connects no
provider accounts; optional [Telegram](telegram.md), [Signal](signal.md), and [iMessage](imessage.md) connectors add live
messages and replies. Sample Telegram, Gmail, Outlook/Georgia Tech, and Slack
conversations demonstrate the shared model. Classification uses simple rules.
Replies to demo conversations are **simulated**, including in Irssi. Connected
provider conversations are labeled live and deliver through the connector.

## Try it

```sh
go build -o bin/amy ./cmd/amy
./bin/amy messages demo
```

This starts the gateway and opens focus in one terminal. Closing focus stops
that gateway. Demo state survives in `~/.local/state/amythest-messages-poc/` (or
`$XDG_STATE_HOME/amythest-messages-poc`). Use `-state /another/directory` for a
separate demo. Seeding is idempotent and does not reset handled conversations.

For a persistent gateway alongside multiple clients, use two terminals:

```sh
./bin/amy messages serve -demo
./bin/amy messages
```

The HTTP API binds to `127.0.0.1:8642`; IRC binds to `127.0.0.1:6669`. Override
with `serve -listen HOST:PORT -irc HOST:PORT`. POC listeners require numeric
loopback addresses; `-irc ''` disables IRC. Clients accept `-url` or
`AMYTHEST_MESSAGES_URL`, plus `-token-file` or `AMYTHEST_MESSAGES_TOKEN`.
Credentials are generated in the state directory as `owner.token` and
`agent.token`; neither is printed. The directory also contains `messages.db`.
Back up the directory if retaining demo data. It is independent of the vault.

## Focus and replies

- Up/down or j/k: choose a conversation; the right pane shows message context.
- `r`: compose a reply to the selected message, or resume a paused composer.
- `n`: start a new reply to the currently selected message (discards the paused
  composer). `Ctrl+D` saves a durable draft before leaving a composer.
- `a`: compose with attachments; `Ctrl+A` opens the picker while composing.
- Space toggles files; Tab returns to the composer. `Ctrl+X` clears attachments.
- `Ctrl+S`: record a demo reply or queue a live Telegram reply. Its account, recipient, thread, parent
  message, and attachment snapshots are preserved. The destination is fixed
  when composition starts, even while incoming messages refresh the queue.
- `Enter`: load context around the selected message in Irssi. Without an IRC
  client, focus remains usable for preview, drafting, and direct replies.
- `d`: handled; `z`: snooze one hour; `u`: reopen; Tab toggles focus/all.
- Conversations sort by their latest incoming or outgoing message, newest first.
  Attention categories remain labels; they do not override recency. All shows
  the latest message in each conversation; Focus retains the incoming reply target.
- `D`: review drafts and delivery records. Enter opens a draft for review;
  `Ctrl+S` sends according to its demo/live destination. Live states refresh.
- Escape returns from a composer without sending. A paused composer lasts only
  for that TUI process. `q` closes focus; `Ctrl+C` exits from any view.

The queue has one item per conversation/thread, keyed by its latest incoming
message. Reply, Don't miss, and Review are deterministic classifications; no
message content is sent to an LLM. Ingesting and opening a message do not mark
it handled. A successful simulated reply or live delivery receipt handles its original parent; a newer
incoming message remains in focus.

## Irssi

Install Irssi with Perl scripting support. In Irssi:

```text
/script load /absolute/path/to/amythest/integrations/irssi/amythest.pl
/amy_connect
/list
```

`/amy_connect` reads the owner token from the default state directory, without
putting it in process arguments. For a different state directory or tunnel:

```text
/set amythest_token_file /absolute/path/to/owner.token
/set amythest_port 6669
/amy_connect
```

The script defaults to the `amy-messages` network tag and loopback host.
`/JOIN` a channel returned by `/LIST`, or select a focus item and press Enter.
The gateway joins the conversation and sends a numeric focus event. The script
activates that conversation's window, then the gateway replays a bounded slice
of stored context with `>>>` marking the selected message. This works even when
that message is no longer in Irssi's scrollback. It is an anchored context replay,
not a search through rendered terminal text.

Typing into a demo conversation records a simulated reply to its most recent
incoming message. In a live Telegram conversation it queues a real reply. For replying to a particular older message, use the focus/API
composer, which pins its parent explicitly. The IRC interface is a minimal
presentation adapter: it supports registration, listing, joining, history replay,
focus, and text replies. It is not a general-purpose IRC network or file-transfer
server. Use Amythest to attach and download files.

If several IRC clients are connected, specify `amy messages -irc-client NICK`.
To focus the containing herdr pane too, pass `-irc-pane PANE` or set
`AMYTHEST_MESSAGES_IRC_PANE`. The pane resolver supports terminal herdr and Herdr
App addresses. Successful pane jumps close the focus TUI. Outside herdr, Irssi's
active conversation changes but the terminal window is not activated for you.

The herdr plugin provides a **Message focus** action and popup. Install the updated
binary with `make install-amy`, then bind your preferred terminal herdr key with
`type = "plugin_action"`, `command = "amythest.messages"`.

For Herdr App, install personal launchers after installing `amy`:

```sh
python3 scripts/install-herdr-app-messaging.py --binary ~/bin/amy
```

This backs up `~/.config/herdr-app/keys.json` and binds **Cmd+Shift+M** to
Messages and **Cmd+Shift+F** to Find. Each opens one full terminal tab; pressing
its shortcut again closes that tool, including from another tab. `q` also
closes the tool. The launcher runs locally even over a remote tab, so private
workstation settings and SSH forwarding remain consistent. It waits for Herdr
to register the terminal before displaying and focusing it. Other bindings are
preserved. Personal absolute paths are generated outside the checkout.

The same installer adds **Cmd+Shift+A** for the [workspace dashboard](workspace-dashboard.md)
and **Ctrl+B, a** for quick task capture. It clones a compatible existing prefix
mod, preserving its other commands.

The older `terminal.open` binding with
`argv: ["/absolute/path/to/amy", "messages", "-pane"]` remains available for
splits in a local tab. Its toggle is scoped to that tab.

## Contacts, service filtering and trash

`p` cycles between all services, Signal, Telegram, the Mac Messages feeder, Gmail,
and Outlook.
It opens All conversations so quiet Signal contacts remain visible. `Tab`
switches between Focus and All. To open one service directly:

```sh
amy messages -all -service Signal
```

Signal contact sync does not invent past messages. A quiet contact shows an
empty-history explanation until an incoming message arrives. Reply composition
requires an imported message as its parent.

`d` marks a message handled, keeping it in All. `x` moves the selected
conversation to **Amythest trash**, hiding it from both Focus and All; later
messages from that conversation stay in trash too. `T` opens/closes Trash,
and `u` restores a selected conversation. This changes the central view only;
provider messages remain available in their source applications.

```sh
amy messages list -trash
amy messages trash -conversation CONVERSATION_ID
amy messages restore -conversation CONVERSATION_ID
```

## Wall items and pasted text

In `amy wall`, select a commit/image/file and press **Shift+S**. Its captured bytes
are uploaded to the messaging attachment store, and the reply picker opens.
Choose the originating conversation, press `r`, add a caption, and send. Demo
conversations simulate delivery; a connected Telegram conversation sends live.
Closing the picker returns to the wall. The original artifact is not modified.
A commit with no captured diff offers a clearly labeled summary file instead.
Files not uploaded to Amythest cannot be attached through the wall.

You can also add an image or pasted text to the shared attachment picker:

```sh
./bin/amy messages attach -file screenshot.png
printf '%s\n' 'Notes from the review' | ./bin/amy messages attach -stdin -name review.txt
```

Attachments are immutable snapshots, up to 5 MiB each, eight per reply, and
64 MiB total for this POC. The API never accepts server filesystem paths. To
save a received/stored attachment locally (existing output files are preserved):

```sh
./bin/amy messages attachment -file downloaded.png ATTACHMENT_ID
```

## Agents

Give an agent only `agent.token`, not the owner's token or the gateway's state
directory. These are API permission boundaries, not a sandbox for a process
that already has unrestricted access to the owner's filesystem.

First discover a target as the owner:

```sh
./bin/amy messages list
```

Grant one conversation for drafting; grants expire and can last at most 24 hours:

```sh
./bin/amy messages grant -conversation CONVERSATION_ID -duration 30m
```

The agent can read only granted conversations and prepare replies:

```sh
./bin/amy messages list -actor agent
./bin/amy messages draft -actor agent -reply-to MESSAGE_ID -text 'Proposed reply'
```

The owner can review it with `D` in focus, or inspect `messages drafts` and then
run `messages send DRAFT_ID`. To let an agent send and attach files in that
conversation, explicitly replace the grant:

```sh
./bin/amy messages grant -conversation CONVERSATION_ID -duration 30m -send -files
./bin/amy messages send -actor agent DRAFT_ID
./bin/amy messages revoke -conversation CONVERSATION_ID
```

Grants are rechecked at send time; creating a draft does not retain authority
after expiry or revocation. Draft creation has an idempotency key, and repeated
send requests do not queue another delivery. There is one agent
identity in this POC; independent named agents and one-use grants are future
work. Attachments must be uploaded by the owner; `-files` allows the agent to
reference selected known attachment IDs, not arbitrary host paths.

## Connector boundary

Authenticated owner-only `POST /ingest` accepts a normalized event. For example:

```json
{
  "conversation": {
    "id": "demo-telegram-personal-robot-thread-42",
    "service": "Telegram",
    "account": "personal (demo)",
    "name": "Robot build",
    "recipient": "robot-group-id",
    "thread": "thread-42",
    "demo": true
  },
  "message": {
    "native_id": "provider-message-123",
    "sender": "Maya",
    "body": "Can you share the patch?"
  }
}
```

Save this as a fixture and run `amy messages ingest -file fixture.json`.
Conversation IDs must include the provider/account/conversation/thread scope.
Replays of the same provider ID within that conversation are deduplicated;
changing a stored conversation's destination is rejected. Incoming attachment
collection and message edits/deletes are not implemented yet.

A conversation with `demo: false` can be ingested and drafted against. Sending
requires a registered live connector and an immutable native route. The personal
Telegram connector supplies that route through its own authenticated ingress.
No provider token, existing Hermes session, or mailbox is automatically reused.
The [Signal adapter](signal.md) uses the same queue, grants and attachment flow.
The [local iMessage feeder](imessage.md) sends Mac messages to the central
gateway. Direct Gmail and Outlook connectors and read-only Claude Slack/Outlook imports
are available. Google Chat adapters remain future work; Discord personal-inbox coverage remains unresolved.

## Private host deployment

The optional gateway binds HTTP and IRC to loopback. Configure the remote host,
local forwards, credential files and selected chats outside the repository using
[Messaging configuration](messages-config.md). The same settings serve the
focus UI and wall sharing. The generic deployment script requires an explicit
SSH host; see [Telegram setup](telegram.md).

## Verification

```sh
go test -race ./...
go vet ./...
```

Tests cover pinned reply destinations across account collisions and refreshes,
restart persistence, replay/send deduplication, expired/revoked agent grants,
attachment snapshots, rejection of unconfigured live sends, HTTP authorization,
IRC focus/context/replies, and TUI sizing. Automated tests use a fake Telegram provider; actual account delivery requires
the interactive login and a live test. Source read receipts are not implemented.

The focus composer and Irssi 1.4.5 were also exercised in real pseudoterminals:
connection, selected-message jumps between conversations sharing a window,
attachment selection, and the simulated reply round trip passed. Both macOS
and Linux ARM64 binaries build successfully.

## Link messages and name contacts

Press **l** on a message to attach its text snapshot to a note or task. Type to
search, Tab switches between all destinations, tasks, and notes, and arrows
choose a result. Enter attaches it. The source stays pinned while new messages
arrive. Task attachments appear beneath the chosen checkbox; note attachments
append a quoted excerpt. Repeating the same message in the same note is rejected.
Changed destination tasks must be selected again. These actions do not send a
reply or change message handling state.

Press **/** to find messages by conversation, sender or latest message text;
submit an empty search to clear it. **f** opens a service picker without switching
Focus to All. **p** retains the older service cycle and includes Slack.

Press **C** to name the selected conversation/contact. Tab completes a known name;
Enter saves it centrally, and an empty name clears the override. Name the same
person consistently across services. Provider refreshes preserve the override,
and names never affect native reply destinations. The names live in the private
messaging database. The CLI equivalent is:

```sh
amy messages contact -conversation CONVERSATION_ID -name 'Morgan Example'
```

For Claude-based Slack/Outlook imports, see [Agent connections](agent-messages.md).
For evidence added to selected tasks, see [Task review](task-review.md).
