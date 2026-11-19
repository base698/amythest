# Personal Signal connector

Signal uses a linked device through [signal-cli](https://github.com/AsamK/signal-cli).
It receives native messages and sends explicitly queued replies to their original
person or group, quoting the original author and timestamp. The same focus UI,
wall attachment picker, agent grants and Irssi presentation work across providers.

## Setup

Deploy the gateway and adapters with `scripts/deploy-messages.sh SSH_HOST ARCH`.
For Linux ARM64, install the pinned runtime on that host:

```sh
~/.local/share/amythest-signal/install-signal-runtime.sh
```

This user-owned installation uses signal-cli 0.14.10, Temurin Java 25 and the
matching libsignal 0.106.1 ARM64 build. Downloads are SHA-256 checked against
published release digests. The native library comes from the additional-platform
builds linked by the [signal-cli native library guide](https://github.com/AsamK/signal-cli/wiki/Provide-native-lib-for-libsignal).
Other platforms can use an existing signal-cli installation through `binary`.

Add this entry to `connectors` in the private
`~/.config/amythest/messages.json` on the connector host:

```json
{
  "signal": {
    "binary": "~/.local/share/amythest-signal/signal-cli",
    "state_dir": "~/.local/state/amythest-signal",
    "token_file": "~/.local/state/amythest-messages-poc/signal.token",
    "all_chats": true,
    "chats": [],
    "send": true
  }
}
```

The gateway URL defaults to `client.url`; set `gateway` to override it.
`send` enables delivery of replies explicitly queued by the owner or an agent
with a current send grant. It does not generate replies. Set it false for
receive-only use. Choose `all_chats: false` and add `user/UUID` or `group/BASE64`
IDs from `amy-signal chats` to limit the scope.

On the connector host, run in a terminal:

```sh
~/bin/amy-signal login
```

Scan the QR code using Signal on the phone: Settings → Linked devices → Link
new device. The command adds the account's stable ACI to private settings. It
links an additional device without registering a replacement primary device.
After login:

```sh
systemctl --user enable --now amythest-signal.service
~/bin/amy-messages connectors
```

Stop the service before login or `amy-signal chats`: one process owns the private
Signal data directory. Account keys never enter the checkout. The connector has
its own gateway token; it cannot claim or complete Telegram deliveries.

## POC behavior and limits

- The connector syncs contacts and groups into **All conversations**, even before
  any message arrives. In focus, press `p` to select Signal; press `Tab` to switch
  between attention and all conversations. Quiet contacts have an empty-history
  state and do not enter attention until a message arrives.
- Focus receives new ordinary messages while the linked device runs. Existing
  phone history is not imported. There is no Signal history backfill; keep the
  service running. The local spool recovers events after they reach the adapter.
- Sent-message syncs from other linked devices appear as outgoing messages and
  update conversation recency without creating attention items.
- View-once and disappearing messages are omitted from persistent focus.
  Reactions, edits/deletes, and story replies are not imported. Received files
  (up to 5 MiB) come with their message from the files signal-cli saves under
  the state directory's `account/attachments/`; larger ones are named instead.
- Replies preserve the original author/timestamp quote. Attachments use the exact
  captured bytes and original filename; up to eight files, 5 MiB each, within the
  gateway's overall attachment quota.
- Delivery is confirmed only when the provider returns a timestamp and success
  for every recipient. Partial group delivery, timeout or interrupted sending
  becomes **uncertain**, with no automatic retry. Inspect Signal first.
- Receipt replay never repeats the provider send. Agent grants are rechecked
  immediately before sending. No source read receipts are sent by this adapter.

Tests cover routing, quoting, attachment bytes, ephemeral-message exclusion,
partial delivery, receipt replay and credential isolation. The full-wire test
uses the actual Go gateway with a fake Signal transport; account delivery still
requires linking and a real message test.
