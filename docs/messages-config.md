# Private messaging configuration

Amythest reads `~/.config/amythest/messages.json` (or
`$XDG_CONFIG_HOME/amythest/messages.json`). This file is installation state,
outside the repository. Use mode 0600 and a private parent directory. Tokens,
provider credentials and sessions live in separate private files, never in git.

Example workstation configuration for a remote gateway:

```json
{
  "client": {
    "url": "http://127.0.0.1:8642",
    "state_dir": "~/.local/state/amythest-remote",
    "token_file": "~/.local/state/amythest-remote/owner.token",
    "ssh": {
      "host": "messaging-host",
      "http_remote": "127.0.0.1:8642",
      "irc_local": "127.0.0.1:6669",
      "irc_remote": "127.0.0.1:6669"
    }
  }
}
```

Use an SSH alias from your private SSH config. Copy the remote gateway's owner
token to `token_file` over SSH and chmod it to 0600. A host-specific script is
unnecessary: `amy messages` and `amy wall` both open/reuse the configured tunnel.
Omit `ssh` for a gateway on the same machine. Set `irc_client` and `irc_pane` in
`client` to select an Irssi nickname and its herdr pane.

Example on the gateway/Telegram host:

```json
{
  "server": {
    "listen": "127.0.0.1:8642",
    "irc": "127.0.0.1:6669",
    "state_dir": "~/.local/state/amythest-messages-poc",
    "demo": false
  },
  "client": {
    "url": "http://127.0.0.1:8642"
  },
  "connectors": {
    "telegram": {
      "credentials_file": "~/.config/amythest/telegram.json",
      "state_dir": "~/.local/state/amythest-telegram",
      "token_file": "~/.local/state/amythest-messages-poc/telegram.token",
      "chats": ["me"],
      "all_chats": false,
      "send": false,
      "initial": 30
    }
  }
}
```

Choose free ports and match the workstation's forwards to them. The connector
uses `client.url` unless its own `gateway` field is set. `chats` is an allowlist
of native IDs as strings, with `me` supported for testing. Restart a connector
after changing its settings. Enable `send` only when ready for queued replies.

Inspect resolved client/server settings with `amy messages config`. It prints
paths and connection settings, not tokens or connector configuration.
`-config PATH` or `AMYTHEST_MESSAGES_CONFIG` selects another file.
Precedence is command-line flags, environment overrides, file, built-in defaults.
Supported environment overrides are `AMYTHEST_MESSAGES_URL`,
`AMYTHEST_MESSAGES_STATE`, `AMYTHEST_MESSAGES_TOKEN` and
`AMYTHEST_MESSAGES_IRC_PANE`. For a different SSH destination, select a separate
configuration. Telegram's CLI uses `--settings` for this shared file and
`--config` for its separate application-credential file.

Repository files contain only generic code, deployment units and examples.
Personal hostnames, phone numbers, selected contacts, API credentials and tokens
must not be copied into those files.

For multiple Gmail mailboxes and Microsoft 365/Outlook, see
[Mail connectors](mail.md). Each mailbox uses a separate private OAuth file and
state directory under `connectors.mail.accounts`.
