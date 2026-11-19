# Slack and Outlook through Claude connections

`amy-agent-messages` imports messages through existing, authorized Claude Code
connections on the machine where those connections are available. This is the
read-only agent transport from the messaging-connector plan. Direct Gmail and
Outlook accounts continue to use [amy-mail](mail.md). Direct MCP transport,
automatic transport selection, native Slack API access and Slack sending are
not implemented by this bridge.

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
deduplicate centrally. Each call imports at most 100 results; this is not an
exhaustive mailbox export. Data passes through the model, so missing, altered or
incomplete model output remains a limitation of the agent route. Source links
and original IDs are retained for checking against the provider.

The bridge serializes Claude startup across accounts, disables hooks and shell
execution for that import, limits the permitted tools to explicit read tools,
checks account/channel scope and validates the entire response before import.
Unavailable connectors and wrong accounts fail without inventing messages.
Messages are durably spooled before ingest; failed ingest is replayable. The
bridge never claims outbound deliveries, sends messages, changes read state,
modifies labels, or trashes source messages. Slack timestamps remain strings.
`amy messages connectors` shows `transport: agent` and `send: false`.

In Messages, **f** filters a service while preserving Focus/All. Slack and Outlook
use the same **l** note/task attachment and **C** contact-name actions as other
services. Any live account setup must remain outside Git.

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
