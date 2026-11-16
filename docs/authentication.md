# Application authentication

Enable `auth_mode: private` to protect the whole application with one browser login.
Private mode currently supports owner browser sessions, read-only agent bearer tokens,
and collector credentials. Optional Work ingest/read/review is documented in
[work.md](work.md). Agent write/control scopes are not implemented yet. Existing off/static/JWT modes remain available for compatibility;
private mode does not combine them or publish a subset of notes.

```yaml
auth_mode: private
auth_public_url: https://notes.example.com/notes
auth_session_ttl: 2160h # 90 days; renewed during active use
# Temporary compatibility for the old MCP bearer; read-only in private mode:
auth_legacy_mcp_token: true
```

Use `AMYTHEST_USERNAME`, `AMYTHEST_PASSWORD` and `AMYTHEST_SESSION_SECRET` (32+ bytes).
The existing `KANBAN_*` names are accepted; conflicting old/new values fail startup.
Existing credentials do not need to change. The first private-mode login replaces the
old cookie. Cookies are HttpOnly, Secure, SameSite=Lax and persist across browser/server
restarts. Sessions expire after approximately 90 days without use, renew at most daily,
and are revoked by logout, Access settings, or a bootstrap credential change.
`auth_session_ttl` accepts 24h–8760h. `auth_allow_http: true` is for loopback-only local
development; never enable it for a deployed site.

Open `/notes/settings/access` to create named reader or collector tokens, approve
registration, inspect expiration/last use, or revoke tokens and browser sessions.
Token lifetime is 90 days, independent of browser renewal. Token secrets are shown once
and stored hashed. Use **Replace token** to issue another secret for the same client
identity, switch the client, then revoke the old credential. The previous secret stays
valid until revoked. Re-registration creates a new client identity; it cannot resume
an existing collector queue.

For an MCP reader, configure the server URL `/notes/mcp` and the HTTP header
`Authorization: Bearer <token>` using the client's secret store. Readers can discover
and invoke note/task/base/board read tools, but not writes, catalog SQL or preview-copy
creation. The reader MCP transport is stateless and reauthorizes every HTTP request;
revocation prevents subsequent requests. HTTP bearer reads are deliberately restricted
to the documented notes/search/tasks and Work read APIs; use MCP for board reads.
A collector token can ingest its own Work stream when Work is enabled, but cannot
read general Work data, notes or MCP.

Browser-assisted registration works on headless machines too:

```sh
amy auth login --server https://notes.example.com/notes \
  --name research-agent --preset reader --output ./research-agent.credential.json
amy auth login --server https://notes.example.com/notes \
  --name laptop --preset collector --output ./laptop.credential.json
```

The command opens/prints a verification link; compare the code and approve while signed
in. The secret returns to the requesting client and is saved in a new mode-0600 file.
Move it to your client's secret store; do not commit it. Pairing expires after ten
minutes and can be exchanged only once. A lost exchange response requires a new request;
revoke the unused credential in Access settings. This is application pairing, not an
OAuth authorization server; generic OAuth “Connect” is not supported yet.

The underlying endpoints are `POST /api/auth/registrations` with `{label, kind}` and
`POST /api/auth/registrations/token` with `{device_code}`. The first returns the
verification URI, user/device codes, lifetime and polling interval. The signed-in owner
approves using the UI; agents cannot approve themselves. Existing `/kanban/api/login`,
`/kanban/api/session` and `/kanban/api/logout` delegate to the same application session.
Cookie-authenticated mutations require the CSRF token returned by the session API and
reject foreign Origin headers. CLI password logins without Origin remain supported.

## Deployment and recovery

`auth.db` in `data_dir` is authoritative. **Do not delete the data directory to rebuild
an index.** Back up auth.db using SQLite's backup facility, preserving live WAL state.
Never copy just a live main database file and assume the backup is complete. Backups
can resurrect revoked credentials; after restoring an older backup, invalidate sessions
and tokens before reopening the service. Private mode uses a distinct migration version
and refuses an auth database from a newer binary.

Before deploying, preserve the current binary and YAML configuration. Keep the existing
systemd unit, listen address, base path and proxy configuration. Set private mode and
the canonical HTTPS URL; explicitly choose whether to retain the legacy MCP token as a
read-only compatibility credential. Restart and check health, anonymous API 401s, note
redirects to login, long-lived Secure cookies, authenticated board/notes access, and MCP
reader denial of writes. If verification fails, restore both binary and configuration
and restart. Do not delete auth.db during rollback.
