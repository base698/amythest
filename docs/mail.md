# Gmail and Outlook connectors

The mail feeder runs beside the central gateway on Linux. Each mailbox has its
own OAuth file, state directory, sync cursors, and immutable reply destination.
The Mac remains only the Apple Messages feeder. No account details belong in git.

The connectors support reading, direct replies from Focus, and attaching wall
items, text, commits, or files to those replies. Replies go to the original
message's Reply-To (or From), from the original account. This is a reply to the
sender, not Reply All. Incoming attachment names are shown; open your mail app to
read/download their contents. Neither connector marks native mail read, deletes
mail, changes labels, or generates automatic replies.

## Install and authenticate

`scripts/deploy-messages.sh messaging-host arm64` installs the gateway, connector
code, and `amythest-mail.service`. It does not copy credentials or enable a new
mail service. Use `scripts/install-mail-connector.sh` on a workstation if needed
for browser sign-in. The connector uses Python's standard library.

Gmail needs a Google Cloud project with the Gmail API enabled and an OAuth client
of type **Desktop app**. Keep the downloaded client JSON outside the repository.
Authenticate each Gmail account separately on the computer running the browser:

```sh
amy-mail auth gmail --credentials ~/.config/amythest/google-client.json \
  --oauth-file ~/.local/state/amythest-mail/personal/oauth.json
```

Sign-in uses a random loopback callback, state validation, and PKCE. It requests
only Gmail read and send scopes. Do not paste tokens into chat. If authentication
happens on a Mac and the feeder runs on Linux, transfer that one private OAuth
file over SSH to its configured location; use mode 0600. Each OAuth file must be
owned by one running feeder. Google projects left in external **Testing** mode
may issue short-lived refresh tokens; follow Google's consent-screen guidance.

Outlook needs your own Microsoft Entra app registration with public client/device
code flows enabled, and delegated `User.Read`, `Mail.Read`, and `Mail.Send`
permissions. The organization may require admin approval. An unrelated app's
client ID is not a substitute. With an approved registration, run on the feeder
host and complete the device sign-in in your browser:

```sh
amy-mail auth outlook --client-id YOUR_APPLICATION_ID --tenant organizations \
  --oauth-file ~/.local/state/amythest-mail/school/oauth.json
```

A specific tenant identifier can replace `organizations`. The app requests
`offline_access` for refresh tokens. No mailbox password is stored.

## Configure accounts

Merge this generic example into the **private** `messages.json` on the feeder
host. Preserve existing connector configuration. Adjust the gateway port and
token paths to the actual installation. The gateway creates separate
`gmail.token` and `outlook.token` credentials in its server state directory.

```json
{
  "connectors": {
    "mail": {
      "accounts": [
        {
          "name": "personal",
          "provider": "gmail",
          "oauth_file": "~/.local/state/amythest-mail/personal/oauth.json",
          "state_dir": "~/.local/state/amythest-mail/personal",
          "token_file": "~/.local/state/amythest-messages-poc/gmail.token",
          "gateway": "http://127.0.0.1:8642",
          "initial_days": 30,
          "initial_limit": 200,
          "send": true
        },
        {
          "name": "school",
          "provider": "outlook",
          "oauth_file": "~/.local/state/amythest-mail/school/oauth.json",
          "state_dir": "~/.local/state/amythest-mail/school",
          "token_file": "~/.local/state/amythest-messages-poc/outlook.token",
          "gateway": "http://127.0.0.1:8642",
          "initial_days": 30,
          "folders": [],
          "send": true
        }
      ]
    }
  }
}
```

Add another Gmail entry for each account with a unique name, state directory, and
OAuth file. Mailbox identity is discovered through the provider and bound to its
journal, preventing a later login from silently reusing another account's state.
`send: true` permits replies that the owner explicitly queues; agents still need
a scoped, expiring grant from the gateway. `send: false` receives only.

```sh
amy-mail doctor
amy-mail sync --name personal
systemctl --user enable --now amythest-mail.service
amy messages connectors
amy messages
```

`--settings PATH` before the command selects another private configuration file;
`AMYTHEST_MESSAGES_CONFIG` is also supported. In the UI, `p` cycles services,
`Tab` switches Focus/All, `r` replies, `Ctrl+A` attaches a file, and `Ctrl+S` sends.
Use `D` for send status. Private server logs include account labels and error
classes, not message bodies, email addresses, or OAuth responses.

## Sync and delivery behavior

Gmail imports up to `initial_limit` recent messages (1–2000) within
`initial_days` (1–365), excluding Spam, Trash, and Drafts. Subsequent polling uses
Gmail history IDs. Expired history triggers another bounded initial import.
Outlook discovers visible folders, excluding Junk and Deleted Items, and keeps a
separate delta cursor for each. `folders` can restrict it to folder IDs or
well-known names such as `inbox` and `sentitems`; `amy-mail folders --name school`
prints discovered IDs. Outlook imports all messages in the date window in those
folders; Gmail's `initial_limit` does not apply to Outlook. Rules that move mail
into other visible folders are covered by the default empty folder list.

Both poll every 30 seconds. Native outgoing mail contributes to recent activity
without entering Focus. Messages are durably spooled before cursors advance;
replays deduplicate centrally. Provider deletion events do not remove the local
archive; Amythest Trash hides a conversation locally.

Gmail replies preserve the native thread ID, subject, Message-ID references and
original account. Outlook replies use the exact original immutable message ID;
its opaque IDs are mapped privately to stable hashed IDs in the gateway. The
connector never resolves a reply by searching a display name or subject.

Attachment bytes come from the gateway's immutable snapshot and their hashes are
verified before sending. Outlook currently limits total reply attachments to
2 MiB; other gateway attachment limits still apply. This leaves room for MIME
encoding inside a simple Graph request; large-file upload sessions are not used.

Gmail returns a sent-message ID. Outlook returns an acceptance without a new ID,
so its status is **submitted**, not confirmed delivery. Outlook's later Sent Items
sync imports the outgoing message; it does not infer a receipt by matching text.
Unconfirmed submissions keep the original message in Focus until handled.

A local receipt is written before reporting to the gateway. A timeout or crash
after a send starts remains **uncertain**, and the same attempt is never retried
automatically. Check the native Sent folder before making a new reply. Revoked
agent grants and changed account/thread destinations block provider sends.

## Provider references

- [Gmail synchronization](https://developers.google.com/workspace/gmail/api/guides/sync)
- [Gmail threads and replies](https://developers.google.com/workspace/gmail/api/guides/threads)
- [Google desktop OAuth](https://developers.google.com/identity/protocols/oauth2/native-app)
- [Microsoft device authorization](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-device-code)
- [Outlook delta synchronization](https://learn.microsoft.com/en-us/graph/api/message-delta?view=graph-rest-1.0)
- [Outlook native replies with MIME attachments](https://learn.microsoft.com/en-us/graph/api/message-reply?view=graph-rest-1.0)
