# Slack and Outlook through Claude connections

`amy-agent-messages` imports messages through existing, authorized Claude Code
connections on the machine where those connections are available. Its Slack
worker also delivers explicitly queued text replies. Direct Gmail and
Outlook accounts continue to use [amy-mail](mail.md). Direct MCP transport,
automatic transport selection and native Slack API access are
not implemented by this bridge.

See the [messaging architecture](messaging-architecture.md), including a PNG and
editable canvas source.

Install on the machine with Claude:

```sh
scripts/install-agent-messages.sh
```

Add accounts to that machine's private `~/.config/amythest/messages.json` under
`connectors.agent.accounts`. Keep the existing `client` settings, SSH tunnel,
provider connectors and credentials. Use a different state directory per account.
The configured gateway must be reachable from this machine. `amy messages` opens
the configured tunnel if necessary.

```json
{
  "connectors": {
    "agent": {
      "accounts": [
        {
          "name": "work-slack",
          "provider": "slack",
          "transport": "agent",
          "account": "T123EXAMPLE:U456EXAMPLE",
          "channels": ["C789EXAMPLE"],
          "query": "in:engineering after:2026-11-18",
          "read_tools": ["mcp__your_slack_server__search", "mcp__your_slack_server__read"],
          "state_dir": "~/.local/state/amythest-agent-messages/slack",
          "token_file": "~/.config/amythest/slack.token",
          "send": false
        }
      ]
    }
  }
}
```

The example IDs/tool names are placeholders. Copy the exact authorized read-only
MCP names from Claude. Wildcards and write/send tools are rejected. The account
is Slack's exact `TEAM_ID:USER_ID`, or the lowercase mailbox email for Outlook.
For Outlook use `provider: outlook`, its connector tools, `outlook.token`, and
omit `channels`. The gateway creates separate `slack.token` and `outlook.token`
files in its private state directory; transfer only the required provider token
over SSH, mode 0600. Do not use the owner's token. Don't run a native and agent
feeder for the same account simultaneously.

```sh
amy-agent-messages doctor --name work-slack
amy-agent-messages sync --name work-slack
```

`doctor` validates local configuration and CLI availability; `sync` verifies the
connected identity and available tools. No background schedule is installed.
Use a bounded query and repeat with overlapping time ranges: provider message IDs
deduplicate centrally. Each call imports up to `limit` results (1–100, default
100), newest first, following search pagination. Filters come from `query`:
`mentions:me` only imports mentions, whereas a date or channel filter can include
ordinary channel traffic. This is not an exhaustive mailbox export. Data passes
through the model, so missing, altered or
incomplete model output remains a limitation of the agent route. Source links
and original IDs are retained for checking against the provider.

The bridge serializes Claude startup across accounts, disables hooks and shell
execution for that import, limits the permitted tools to explicit read tools,
checks account/channel scope and validates the entire response before import.
Unavailable connectors and wrong accounts fail without inventing messages.
Messages are durably spooled before ingest; failed ingest is replayable. The
import command never claims outbound deliveries, sends messages, changes read state,
modifies labels, or trashes source messages. Slack timestamps remain strings.
`amy messages connectors` shows `transport: agent` and the configured send flag.

In Messages, **f** filters a service while preserving Focus/All. Slack and Outlook
use the same **l** note/task attachment and **C** contact-name actions as other
services. Any live account setup must remain outside Git.

Slack author IDs are stored separately from display names. The import resolves
names with the configured profile tool; replay enriches older ID-only messages
without replacing their content or reopening handled messages. **m** searches the
people in the current conversation and opens the chosen person in Slack. When a
team ID is known this opens a DM; otherwise it opens their profile in the source
workspace, where **Message** starts a DM. **o** opens the original message.
These shortcuts do not send anything.

## Queued Slack replies

Run the worker on the machine with the authenticated Slack connection in Claude
Code. That can be a Mac or a Linux node; it does not need to be the machine running
the Amy UI or gateway. Claude Code **2.1.295 or newer** is required for fail-closed
hooks. The connected Slack tool must support `slack_send_message`; do not replace
it with an upload or other side-effecting tool.

Add the exact `send_tool` and `profile_tool` names to the private Slack account:

```json
{
  "send_tool": "mcp__your_slack_server__slack_send_message",
  "profile_tool": "mcp__your_slack_server__slack_read_user_profile"
}
```

With `send` still false, run the read-only binding check:

```sh
amy-agent-messages send-doctor --name work-slack --bind
```

This saves a fingerprint of the connected profile in the private settings file.
After checking that Claude is signed into the intended workspace, set `send: true`.
Run `send-doctor` without `--bind` to verify an existing binding. A changed
identity requires reviewing the connection and explicitly rebinding; the worker
never silently switches accounts.

```sh
amy-agent-messages watch --name work-slack
# Or install a login service (macOS LaunchAgent / Linux user systemd):
python3 scripts/install-agent-worker.py --name work-slack
```

The installer starts the worker and prints its service path. Settings, connection
identity, token, logs and generated service stay outside Git. On Linux, a user
service normally follows the login session; configure lingering separately if it
must run after logout. The gateway or its private tunnel must remain reachable.
The worker polls the gateway without invoking Claude while idle; it invokes
Claude only for a claimed send. Imports still run separately with `sync`.

In an imported Slack conversation, **r** composes a reply and **Ctrl+S** submits
it. The composer shows whether it posts in a channel/DM or an existing thread.
The current limit is text only, up to 5,000 characters. **D** shows delivery
status. Starting a conversation with a new recipient is not implemented; **m**
can open a person's Slack profile/DM in Slack itself.

An explicitly submitted reply queues even while a previously registered,
send-enabled worker is offline. Disabled, unregistered or out-of-scope accounts
keep the reply as a draft. Returning workers recheck permissions before claiming
and immediately before sending. Agent sends still require an owner-issued grant.
The queued destination, body and thread cannot be changed by the model. A command
hook checks the bound profile and exact arguments and permits one send call;
other mutations are blocked. The worker uses the provider tool's native receipt,
never the model's claim that it sent something. Ambiguous sends become
**uncertain**, and are never automatically retried. A durable local journal
replays completion receipts after a gateway disconnect without repeating sends.

Hook behavior is documented in [Claude Code hooks](https://code.claude.com/docs/en/hooks).
The `onFailure: block` setting is required: a hook timeout or crash must prevent
the send rather than allow it through.

## Outlook through Claude in Chrome

For a machine where the Outlook MCP connection cannot be authorized, add two
separate private accounts using `transport: chrome`: `provider: outlook-web`
for mail and `provider: outlook-calendar` for calendar. They appear as **Outlook
(Chrome)** and **Calendar (Chrome)** in the Messages service picker.

```json
{
  "name": "browser-calendar",
  "provider": "outlook-calendar",
  "transport": "chrome",
  "url": "https://outlook.cloud.microsoft/calendar/view/workweek",
  "timezone": "America/New_York",
  "state_dir": "~/.local/state/amythest-agent-messages/calendar",
  "token_file": "~/.config/amythest/outlook-calendar.token",
  "send": false
}
```

For mail, use `outlook-web`, your private Outlook mail URL, a separate state
directory and `outlook-web.token`. `account` may be the expected lowercase email;
when omitted, the first verified visible mailbox identity binds the private
journal. A later account change is rejected. `read_tools` isn't needed for Chrome:
the bridge fixes its browser tool set to tab discovery and page reading.

The browser must already have the relevant Outlook mail and calendar pages open
and signed in, with Claude-in-Chrome enabled. The collector reads those existing
tabs; it does not open a window, navigate, click messages, type, or run JavaScript.
If only a snippet is visible, the imported item is explicitly a visible snapshot,
not a complete message. Mail rows need an observed message permalink. Calendar
snapshots retain start/end times with offsets, timezone, all-day status, location
and status. Repeated snapshots deduplicate; changed events produce a new version
when an event ID is available. If the page exposes no native event ID, identity
uses title/start/organizer and is labeled accordingly; a reschedule can appear as
a new item. Missing events are never assumed cancelled.

This route is intentionally limited to visible content. It can fail when Outlook
changes its layout, the tab isn't available, the mailbox identity is hidden, or
Claude/Chrome needs interactive setup. It does not fall back to an empty inbox.
Setup reference: [Claude Code with Chrome](https://code.claude.com/docs/en/chrome).

Different machines may use different routes: one can run the Outlook MCP bridge,
another the Chrome bridge, both feeding the same private gateway. Browser
snapshots use distinct source IDs; importing the same mail through both routes
can show both snapshots. Filter by service to keep them separate.

Some hosted Slack connectors expose the current user profile and workspace name
but no native workspace ID. For those, configure `slack_user` and `workspace`
from the observed profile, and set `account` to `profile/` plus SHA-256 of
`slack_user + "\0" + workspace`. Every import must return that same observed user
and workspace. This is a local connection identity, not an invented Slack team
ID; native message/channel timestamps remain unchanged. Configuration and these
identity values stay private.
