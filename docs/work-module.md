# Work module — implementation plan

Status: implementation in progress (2026-11-16). Shared authentication is committed
and deployed. The first optional Work read/review slice is implemented; see
[work.md](work.md) for its actual configuration, API, limits and remaining cutover
gates. Amythest Collector has a durable shadow sender for the local herdr TUI. The full plan below
remains the target, not a claim that every capability has shipped.

Implemented: private application auth, durable/revocable 90-day browser sessions,
Access settings, reader/collector bearer credentials, browser-assisted pairing,
`amy auth login`, identity-preserving credential replacement; durable Work streams,
bindings, runs, artifacts, reduced-message storage, verified media, review, attention,
snooze, manual filing, scoped Work search and MCP reads, card Work tab and board badges.
Amythest Collector registration and a persistent event queue retain local JSONL during shadow use.

Not yet implemented: public-note publishing, private-mode external JWT mapping,
writer/control scopes, recoverable board completion, queued commands, collector
transcript reduction/full retrieval and automatic media delivery, merged notes search,
historical backfill and complete retention/restore/cutover tooling. Existing legacy
auth modes remain available; shipped authentication uses flat `auth_*` config keys.

Work shows what coding agents (and other agents, robots, devices) did for each kanban
card: their state, runs, commits, PRs, images, videos, notes and messages, with search
across the output. Amythest remains the system of record for what should happen;
Work records what happened. Agents execute elsewhere, under herdr / herdr-core.

## Goals and boundaries

- Show agent activity across machines, including machines that sleep.
- File output under the correct card even after offline replay or pane reuse.
- Provide a clear “Needs you” list with explicit review and snooze actions.
- Search notes, transcripts, artifacts and linked messages through the UI and MCP.
- Use one application sign-in and individually revocable credentials for machines
  and agents, with browser-assisted registration.

Work makes no model calls and does not schedule agents. Boards remain markdown
canonical. Work never edits `board.md` directly: explicit card actions use the
existing locked, journaled board store. Passive ingest never moves a card.
Authentication is an application capability, independent of enabling Work. Initial
scope is one owner and that owner's devices/agents, not multiple users or tenants.

## Configuration and durable storage

Work is off unless enabled. Names and deployment URLs are configuration, not code.

```yaml
auth:
  public_url: https://notes.example.com      # canonical deployed URL, including base path
  db: ~/.local/share/amythest/auth.db
  public_notes: false                      # private application by default
  session_ttl: 2160h                       # 90 days, renewed during active use
  token_ttl: 90d                            # owner can choose up to configured maximum
  registration_ttl: 10m
work:
  enabled: true
  db: ~/.local/share/amythest/work.db
  media_dir: ~/.local/share/amythest/work-media
  media_max_file: 50MB
  media_max_total: 20GB
  media_overflow_dir: ""                   # optional managed overflow store
  stall_after: 45m
  asleep_after: 3m
  retention: 180d
  command_ttl: 15m
```

Use `AMYTHEST_USERNAME`, `AMYTHEST_PASSWORD` and `AMYTHEST_SESSION_SECRET` for
bootstrap sign-in. Secrets stay out of YAML. Registered collectors and readers live
in auth.db; configuring an environment token for every machine is no longer required.

The existing index is disposable and can be deleted on a schema change. auth.db and
work.db are durable SQLite stores with forward-only `PRAGMA user_version` migrations,
WAL and foreign keys enabled. Neither follows the index's delete-and-rebuild path.
Use explicit durable commit settings for acknowledged ingest. Fail startup on an
unsupported newer schema; migration failure preserves the existing database.

## One authentication and authorization model

### Browser sign-in

Provide `/login`, `/api/auth/login`, `/api/auth/logout`, `/api/auth/session` and
`/settings/access`. A browser signs in once for notes, search, kanban, Work, files and
settings. Redirect unauthenticated HTML requests to login with a validated local
return path; APIs return 401 instead of login HTML. Missing scope returns 403.

Use a root-path HttpOnly, Secure, SameSite cookie on deployed HTTPS, CSRF tokens and
Origin checks on cookie-authenticated mutations, and rate limits on sign-in. Persist
revocable session records in auth.db; logout invalidates the session server-side.
Rotate the session on login; changing bootstrap credentials invalidates old sessions.
Only explicit loopback development may use HTTP. Honor forwarded origin/address
headers only from configured trusted proxies.

One shared middleware authenticates the request into a principal (owner session,
collector, agent token, or mapped external JWT); shared policy checks authorize each
operation. Remove the separate kanban-only gate and the outer bearer-only guard that
would reject valid browser cookies. An explicitly supplied invalid bearer token
fails rather than falling back to a cookie. Bearer-only requests do not need CSRF.

Default-private means all content routes, previews, downloads, search results,
transcripts, media and MCP require authorization, including older note, asset,
presentation, catalog and kanban endpoints. Login resources, registration initiation
and polling, and minimal liveness/readiness responses are narrow exceptions; metrics
and detailed diagnostics remain protected. Private responses use private/no-store
caching as appropriate and must not enter shared caches.

`public_notes: true` is an explicit notes-publishing mode: only approved note reads,
associated public assets and notes-only search are anonymous. It does not expose
kanban, Work, vault mutations, catalog data, private attachments or token management.
Public search never merges Work results. Define and test the route allowlist; there
is no blanket anonymous `/api/*` or `/assets/*` exemption for private material.
Before enabling this mode, define public-note eligibility and enforce it in embedded
notes, task/base queries, backlinks, navigation and search. Cache public and private
renderings separately; an allowed route must never inline private content. This
publishing mode is not enabled by the initial private-mode implementation.

### Credentials and scopes

Tokens are opaque high-entropy secrets, sent only as `Authorization: Bearer …` over
HTTPS. Persist a token identifier, secret hash, owner, client identity/type, scopes,
resource audience, creation/expiry/revocation times and last use; never persist the
plaintext bearer secret. Token labels are display text, not identity or authority.
Audit credential lifecycle and privileged actions without recording credentials.

| Principal / preset | Allowed operations |
|---|---|
| Owner browser session | Application reads and writes; credential management; review; card completion; explicit approval/denial |
| Collector | `collector:ingest`, `collector:media`, `collector:commands` for its immutable collector ID only; no general notes/Work search or MCP access |
| Agent reader (default) | `notes:read`, `kanban:read`, `work:read`; reads/search through MCP and corresponding read APIs; no mutations or live pane reads |
| Agent writer (explicit opt-in) | Reader scopes plus selected `notes:write`, `kanban:write`, `work:write` |
| Agent controller (separate opt-in) | `work:control` plus `work:read` for queued prompt/live-read; never approval or denial |

Scopes apply inside MCP tool handlers as well as HTTP handlers. Filter tool discovery
and independently enforce scopes on invocation, resources and batch requests. Inventory
all existing tools/routes: no implicit “authenticated means all tools.” Catalog reads
have a separate `catalog:read` scope, absent from the reader preset. Token audiences
are bound to this installation and its permitted API resources; a collector cannot
claim another collector ID in a request body or poll another machine's commands.
Agents cannot mint tokens, expand scopes, approve devices or impersonate a human.

Settings lists named sessions/credentials, scopes, expiration and last use, with
revoke and replace actions. Default token lifetime is 90 days; warn ahead of expiry.
Replacement issues a new secret and lets the owner revoke the old token after the
client switches. Rotation preserves collector identity and queued-event numbering.
Revocation applies to each request and active long polls/streams; it also cancels
undelivered commands issued by that credential. Already executed actions remain audited.

### Register a collector or MCP reader by opening a link

Proposed CLI entry points:

```text
amythest-collector register --server https://notes.example.com --name laptop
amy auth login --server https://notes.example.com --name research-agent --preset reader
```

1. The client creates a registration request at `POST /api/auth/registrations`,
   specifying its type, label and requested scopes. The server returns a secret
   device code, short user code, verification URL, expiry and minimum polling interval.
   Apply quotas/rate limits; creating a pending request grants no access.
2. The client opens the verification URL or prints it for use on a phone/another
   machine. The URL may carry the user code, never the device code or bearer token.
3. The deployed site reuses the owner's sign-in, or signs them in and returns to the
   pending request. Show the code to compare with the requesting terminal, device or
   agent name, requested permissions and expiration. A link GET never approves;
   require an explicit CSRF-protected Approve or Deny action. The owner may narrow
   scopes; the client cannot change the approved request afterward.
4. The client polls `POST /api/auth/registrations/token` using the secret device code.
   Return pending, slow-down, denied or expired until approval. After approval, exchange
   the code atomically for the new scoped token and immutable client ID. Polling uses
   the advertised interval and backs off; concurrent exchanges issue at most one token.
5. Store the credential in the client OS credential store, or a mode-0600 file on a
   headless host. If the exchange response is lost, re-register; settings exposes the
   unused credential for revocation. Never weaken single-use exchange to recover it.

The consent and polling behavior follows the device-flow pattern in
[RFC 8628](https://www.rfc-editor.org/rfc/rfc8628.html). These `/api/auth/registrations`
endpoints are an application pairing API, not a claim of complete OAuth support.

For MCP clients supporting configured HTTP headers, Settings also offers “Create agent
token”: choose the Reader preset and expiration, then copy the secret once into the
client's secret/environment configuration for `/mcp`. Show the server URL and header
format without embedding a live token in a URL, repository file or log. The CLI pairing
flow yields the same kind of credential; it does not depend on the MCP client having a
browser. Never share the collector credential with an agent.

Automatic “Connect” in arbitrary OAuth MCP clients is a separate interoperability
milestone. It requires the discovery metadata, authorization-code/PKCE and resource
validation described in the [MCP authorization specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization).
Do not advertise OAuth discovery or assume these clients understand custom pairing
until that flow is implemented and tested. Configured bearer-token readers are in v1.

### Migration from existing authentication

- Promote `internal/kanban/auth` into the shared auth service; kanban mounting and
  board-store availability no longer depend on kanban-specific login credentials.
- Accept old `KANBAN_USERNAME`, `KANBAN_PASSWORD`, `KANBAN_SESSION_SECRET` and
  `KANBAN_COOKIE_SECURE` as deprecated aliases for one release. New names win only
  when unambiguous; reject conflicting values. Old login/session paths delegate to
  the shared service. Require one fresh login on upgrade to the revocable session.
- Replace `auth_mode` / `auth_protect` with explicit application policy and credential
  providers. Do not silently interpret old `off` or `protect: mcp` as permission to
  publish private Work. Deployments select private mode or explicit public-notes mode
  during migration. Private mode without a configured sign-in method fails startup.
- Keep `AMYTHEST_MCP_TOKEN` only behind an explicit temporary legacy-token setting,
  mapped to a named agent with configured scopes (reader by default). No implicit
  writer/admin fallback. Document the write-scope change before rollout and register
  replacement agent credentials before disabling the compatibility setting.
- Preserve optional external JWT authentication by mapping verified issuer/subject,
  audience and allowed claims into the same principal/scopes. Require issuer and
  audience for this provider; unmapped claims grant nothing. A JWT does not acquire
  owner-browser privileges. Existing JWT deployments must supply the mapping.

## Identity, binding and state

| Term | Meaning |
|---|---|
| Collector | Immutable registered client ID; display name may change. One active producer generation. Queues durably while offline. |
| Generation | Server-registered stream ID; sequence starts at 1. Survives normal restarts and token rotation; new only after explicit queue reset/recovery. |
| Machine | Collector host with server-observed last contact; online, asleep/unreachable, or explicitly off. |
| Slot | `(collector_id, pane)` address; not the identity of the agent currently there. |
| Session | Collector-generated UUID for one agent incarnation; pane reuse creates a new session even if the pane name matches. |
| Binding | Versioned assignment of a slot/session to a card; history retained. At most one active binding per slot. |
| Run | Collector-generated UUID for a stretch of work in a session; immutable binding snapshot. |
| Artifact | Output identity independent of how many runs/cards reference it. |
| Project | Workspace label for grouping, including output without a card. |

Explicit `run.start` / `run.finish` events are authoritative. Adapters that only see
status transitions synthesize those events once and persist their IDs in the queue.
`slot.status` changes display state but never creates or finishes a run. A run has
active/finished/abandoned lifecycle and a separate outcome (success/failure/cancelled/
unknown); “done” is a card declaration, not a run outcome. Slot loss abandons an active
run only when explicitly confirmed; machine sleep alone never finishes a run.

At run start the collector snapshots `binding_id` and `binding_version`, acknowledged
through its control poll. Binding changes apply to future runs; explicit mid-run
switching ends the old run and starts a new one. A collector that has not received a
rebind keeps attributing existing work to its recorded binding; UI shows the new
binding as pending. Never reinterpret old output against the current binding.
Run-dependent events carry `session_id`, `run_id` and the binding snapshot. Unknown
or unavailable attribution stays unfiled with a reason/suggestion, not silently filed
under today's card. Corrections add audited associations without rewriting provenance.

Rebinding and pane replacement cancel queued commands for the old target. Binding
updates use compare-and-swap versions; stale acknowledgements cannot replace newer ones. Binding
state is authoritative in work.db; optional herdr labels are hints, not recovery data.
Resolve current board location by stable card ID, including archive/cross-board moves.
Deleted cards retain historical links marked deleted; never attach output to a new
card that happens to share the title.

Store both collector occurrence time and server receipt time. Stream sequence, not
wall-clock ordering across machines, determines application order. Heartbeat liveness
uses a fresh authenticated heartbeat/control poll received now; replayed heartbeat
payloads never claim recent agent output. On reconnect, display “syncing” until the
collector's fresh inventory and backlog catch up. Only then evaluate stalls from a
fresh report of output inactivity, preventing old backlog or clock skew from causing
false stalled alerts. Sleep is shown as connectivity status separate from agent state.

## Data model

The following is logical schema; migrations must add foreign keys, indexes, uniqueness
constraints and checked enums, rather than treating this shorthand as executable SQL.

```text
auth.db
  clients(id PK, owner, type, label, created_at, revoked_at)
  tokens(id PK, client_id, secret_hash, scopes, audience, expires_at, revoked_at, last_used)
  sessions(id PK, owner, secret_hash, csrf_hash, expires_at, revoked_at)
  registrations(id PK, device_code_hash, user_code_hash, request, state, expires_at, token_id)
  auth_audit(id PK, actor, action, target, at, result)

work.db
  machines(collector_id PK, name, last_contact_at, version, connectivity)
  streams(collector_id, generation, acked_seq, state, PK(collector_id, generation))
  slots(id PK, collector_id, pane, current_session_id, status, status_since, last_output_at)
  sessions(id PK, slot_id, agent_kind, provider_session_ref, workspace, tab)
  bindings(id PK, slot_id, session_id, version, card_id, bound_at, unbound_at, acknowledged_at)
  runs(id PK, session_id, binding_id, binding_version, card_id, started_at, finished_at,
       lifecycle, outcome, output_version, finished_version)
  events(collector_id, generation, seq, occurred_at, received_at, kind, payload, payload_hash,
         disposition, PK(collector_id, generation, seq))
  artifacts(id PK, identity_key UNIQUE, kind, ref, title, repo, metadata_version, created_at)
  artifact_links(id PK, artifact_id, run_id, card_id, project, source_collector,
                 attribution, suggested_card_id, featured, filed_by, created_at)
  media(sha256 PK, store, relative_path, bytes, availability, last_access_at)
  artifact_media(artifact_id, sha256, role, remote_collector, remote_path)
  chat_turns(id PK, run_id, session_id, role, text, occurred_at, output_version)
             -- role: user | assistant | tool_call (one-line summary, no output)
  reviews(owner, run_id, reviewed_through_version, reviewed_at, PK(owner, run_id))
  attention(id PK, subject, reason, version, resolved_at, snoozed_version, snoozed_until)
  commands(id PK, request_key UNIQUE, issuer, collector_id, session_id, run_id,
           binding_version, approval_request_id, kind, payload, expires_at, state, result)
  command_attempts(id PK, command_id, leased_at, started_at, finished_at, result)
  done_operations(id PK, request_key UNIQUE, actor, card_id, run_id, summary,
                  expected_card_version, state, board_receipt, error)
  done_marks(operation_id PK, card_id, run_id, actor, summary, at)
  work_fts USING fts5(key UNINDEXED, kind UNINDEXED, title, body, tags,
                     tokenize='porter unicode61 remove_diacritics 2')
```

FTS is a rebuildable projection over retained source records, not the transcript store.
Filter card/project/date through joins to source records; don't duplicate or overwrite
an artifact's text for every card association. Keep command/audit history distinct
from the retained raw ingest stream. Collector IDs are validated against auth.db by
the service; cross-database foreign keys are not assumed. Validate ownership of every
session/run/binding/media reference, not just the collector field: another collector
cannot mutate existing IDs or attach itself to another collector's run.

### Artifact identity and enrichment

Commit identity is canonical repository identity plus full SHA; PR identity is its
canonical service/repository/PR ID. File observations use collector ID plus a persisted
file-observation UUID (path and mtime are metadata, not globally unique identity).
Blob SHA-256 deduplicates bytes separately. A shared commit/blob can have multiple
run/card associations without overwriting any association.

Unique source-event/link constraints prevent replay from creating duplicate links.
Newer metadata versions may enrich titles, captions or upload availability; a repeated
identity is not an instruction to discard new information. Collector updates never
overwrite manual filing, captions or featured flags. Suggestions occupy a separate
field and remain unfiled until accepted. Explicit add/move/remove association actions
are audited; removing one association leaves others intact.

## Ingest and media contracts

`POST /api/work/streams` creates/resumes the authenticated collector's generation.
Generation and next sequence are durable locally. Token rotation resumes that stream;
a lost queue needs an explicit reset, marking the old stream interrupted and its
unfinished runs unresolved. Reject events from a closed generation. Never silently
accept a sequence reset under an existing generation.

`POST /api/work/events` uses the token's collector identity:

```json
{"generation":"gen-uuid","events":[
  {"seq":1041,"at":"2026-11-16T11:06:02Z","kind":"run.start",
   "session_id":"session-uuid","run_id":"run-uuid",
   "binding_id":"binding-uuid","binding_version":3},
  {"seq":1042,"at":"2026-11-16T11:06:03Z","kind":"artifact",
   "session_id":"session-uuid","run_id":"run-uuid",
   "binding_id":"binding-uuid","binding_version":3,
   "artifact":{"identity_key":"commit:repo-id:full-sha","kind":"commit","title":"Fix gait"}}
]}
```

- `acked` is the highest **contiguous, durably committed** sequence, never the maximum
  seen sequence. Validate the batch and commit events, projections and watermark in
  one transaction before responding. A crash before commit is retried; after commit
  a lost response is a harmless replay.
- Start at `acked + 1`; ignore the already-acknowledged prefix. Reject a batch with a
  gap or internal disorder with `409 {acked, expected_seq}` and no new writes. If
  1043 arrives before 1042, ack remains 1041. Serialize application per stream.
- Bound batches (initially 500 events / 2 MiB). Return 422 with the failing sequence
  for malformed/unsupported events, applying none of the new batch. Amythest Collector keeps
  the queue, surfaces the error and can retry a valid prefix; it never auto-skips.
  An owner-authorized quarantine action records a rejection/tombstone for exactly
  the next sequence with reason/hash before advancing. Dependent missing references
  then remain unresolved, rather than being assigned to a different run.
- Successful response is `{"generation":"gen-uuid","acked":1042}`. Amythest Collector durably
  removes only the acknowledged prefix for that generation. Retain stream watermarks
  and closed-generation tombstones beyond raw-event retention; old replay stays inert.
- New events use immutable IDs; duplicate ID with conflicting content is an error
  while retained. Already-pruned acknowledged events are ignored, never reapplied.

Kinds: `slot.seen`, `slot.status`, `slot.gone`, `run.start`, `run.finish`, `artifact`,
`chat`, `observation`. Fresh `POST /api/work/heartbeat` carries inventory/sync status
and output inactivity, independently of replay. Backfilled JSONL is historical import,
with deterministic source IDs and no live-state/notification effects.

`PUT /api/work/media/{sha256}` uploads bounded bytes under collector scope. Verify
actual hash and length, write to a temporary file, then atomically publish; repeated
uploads are safe. Artifact metadata and bytes can arrive in either order; use pending
references and garbage-collect unreferenced uploads after a grace period. Rejected or
oversized uploads do not invalidate artifact metadata. Serve media through an
authorized handler with safe content types/disposition, thumbnail limits and range
support; never expose arbitrary host paths or execute uploaded HTML/SVG as app content.

`remote_path` denotes a file on a named collector, not a server path to open. Oversized
or evicted media remains a visible metadata record (“on laptop” / “evicted”). Evict
unfeatured blobs first and protect any blob referenced by a featured link; if all
remaining media is protected, reject/defer uploads rather than exceed quota. Overflow
uses the same managed addressing/authorization, with explicit availability on mount
failure. `POST /api/work/uploads` gives owner sessions or `work:write` agents bounded upload
handles using the same blob validation; handles are bound to the issuing principal.
`work_attach` accepts those upload handles or stores external URLs as links; it
never reads a remote agent's path from the server filesystem or fetches arbitrary URLs.

## Review, Done and Verify

Run finish only adds an attention item; it never moves a card. Every new output and
run-finish event advances that run's `output_version`. “Review” records the version
actually displayed, not a newer value arriving concurrently. A finished run needs
review until its finish and output versions are acknowledged. Later output reopens
attention. Viewing a card alone does not acknowledge it; reviewing does not move it.

A person may move a card to Verify, or an authorized writer calls
`work_done(card, run_id, summary, request_id, expected_card_version)`:

1. Validate the run/card relationship and expected board revision. Persist a durable
   `done_operations` intent, unique by actor/request ID; conflicting reuse is rejected.
2. Apply one board-store operation that appends the summary and moves the card to
   Verify under its lock/journal. Persist the operation receipt in the same canonical
   board transaction as the changes. This requires extending the board-store API;
   separate existing AddComment/MoveCard calls are insufficient.
3. Finalize the operation and `done_marks` in work.db. After a crash, reconcile the
   board receipt before retrying. A pending operation is shown as pending, not done.
   The same request never duplicates a comment or reverses a later human change.

A changed/deleted/archived card produces a conflict requiring a fresh decision; do
not reopen or move it silently. Only finalized declarations appear as declared done.
Run summaries may be deliberately posted to the vault; raw chats/messages are not.

Human-only completion is an enforced policy across UI, HTTP and MCP: agent principals
cannot transition a card to Done or archive it through existing `kanban_update_card`,
`kanban_move_card`, `kanban_archive` or equivalent alternate paths. Agent note/file
write tools must also reject canonical board/archive paths; all board mutations pass
through the store. Agents with direct vault filesystem access are outside this API
boundary. Test the rule at the shared service layer, not just by hiding tools.
Completing a card acknowledges the run versions visible in that action; later output
still needs review, including on archived cards.

Attention items are derived by subject/reason/version. Snooze applies only to the
current version until a chosen time; a new approval request or output version breaks
it. Blocked state clears when its exact request is resolved. Offline machines show a
single connectivity item rather than a stall per slot; stale agent state remains
visible with its age. On reconnect, wait for sync before raising stall alerts. With
multiple runs on a card, show counts and prioritize blocked, stalled, needs-review,
working, then idle; connectivity remains a separate badge, never a fabricated “done.”

## Talking back to agents

`POST /api/work/commands` creates a command with an idempotency key and explicit
collector/session/run/binding-version target. `GET /api/work/commands?wait=30s` leases
only the authenticated collector's commands. Use a separate control stream for
binding updates, acknowledged by binding ID/version.

| Command | Permission / precondition |
|---|---|
| `prompt` | Owner or `work:control`; exact current session/run/binding |
| `read` | Owner or `work:control`; same target; bounded last-N-lines response |
| `approve` / `deny` | Owner browser only; exact pending approval request ID and payload digest |

Approval/denial additionally requires the collector adapter to expose a stable request
identity and atomically check that request while responding. Generic terminal keystrokes
cannot provide this guarantee: adapters without it show Open, with remote approval
unavailable. `prompt` cannot be used to answer a pending approval; reject it while
blocked and recheck at execution. The service does not promise to constrain an agent
that independently has direct control of the host terminal.

Command lifecycle: queued → leased → executing → succeeded/failed/unknown, with
expired/cancelled/stale as terminal alternatives. Collector persists command IDs and
results locally, rechecks expiry and target immediately before execution, and posts
results to `POST /api/work/commands/{id}/result`. Lease timeout permits redelivery of
unstarted commands. Lost result responses are retried idempotently.

Do not claim exactly-once external terminal effects. If a collector crashes after
marking execution but before durably recording the result, mark outcome unknown and
require a fresh human decision; never automatically replay a possibly executed prompt
or approval. Use adapter idempotency keys when supported. Queued commands expire,
rebind/session changes invalidate them, and retries cannot retarget them. Audit actor,
target, request, execution attempts and result. Pending/expired/unknown are visible in
the UI. Cancel undelivered commands on credential revocation and check live authorization
before dispatch; an action already in flight cannot be recalled reliably.

## Search and MCP

Existing notes search uses the index retrieval APIs. Work adds a separate FTS
projection of chat turns (including tool-call lines), commit/PR text, artifact
captions/text (bounded), linked messages, done summaries and observations. Retained source rows provide context links.

`GET /api/work/search?q=&scope=` and `work_search(q, scope)` share authorization and
query parsing. Support combined card/project/kind/date filters; card scoping includes
explicitly linked notes only, never all notes as a fallback. Preserve archived-note
filter semantics. Merge per-source rankings with reciprocal-rank fusion and stable
ID tie-breaking, not raw BM25 scores from unrelated databases. Define bounded result
windows and cursor semantics before pagination; apply authorization/filtering before
ranking, counts and snippets. Refiling updates scope immediately without duplicating
search hits. Chat hits link to a stable turn ID; pruned context displays “expired.”

| MCP tool | Scope / effect |
|---|---|
| `work_inbox()` | `work:read`; attention items |
| `work_card(card)` | `work:read`; agents, runs, links and review state |
| `work_search(q, scope)` | Corresponding read scopes for every included source |
| `work_bind(card, machine, session_id, expected_binding_version, request_id)` | `work:write`; versioned future-run binding |
| `work_file(link_id, card, mode, request_id)` | `work:write`; explicit association change; suggestions do not file |
| `work_attach(card, upload_id_or_url, caption, request_id)` | `work:write`; attach uploaded media or an external link |
| `work_done(card, run_id, summary, request_id, expected_card_version)` | `work:write` + `kanban:write`; recoverable Verify operation |
| `work_prompt(target, text, request_id)` / `work_read(target, lines, request_id)` | `work:control` + `work:read`; queued command |

Review/snooze/completion and approval remain owner actions in v1; readers cannot clear
the owner's inbox. All write tools have request deduplication and expected versions
where concurrent changes matter. No MCP tool accepts an arbitrary `actor` or human flag.

## Views

1. **Application sign-in and Access settings.** One login; register named collectors
   and agents, inspect permissions/expiry, revoke or replace credentials.
2. **Board cards.** State counts, connectivity badge, new output counts and up to three
   thumbnails. Expose multiple runs instead of implying a single agent owns the card.
3. **Card Work tab.** Agents, timeline, media players, scoped search, review and binding.
4. **Needs you in Focus.** Blocked, finished-unreviewed, stalled and connectivity items;
   version-aware Review/Snooze, safe commands, clear pending and unknown outcomes.
5. **Project page.** Workspace tabs/cards, activity strip and media gallery.
6. **Unfiled tray.** Suggestions with provenance; File or New card from this run.
7. **Optional daily note line.** Debounced summaries and private Work links only;
   explicitly enable this vault export because a vault can be publicly published.

## Retention, recovery and privacy

Raw chat/message text lives only in work.db; explicit summaries/comments are deliberate
vault exports. Collectors support exclusion rules and bounded text before forwarding;
see "Transcript capture" below. Retention removes raw events/chat text after
180 days and removes their FTS entries in the same operation. Artifact metadata linked
to cards remains; unfiled artifact metadata expires after 180 days unless pinned, command result text
expires after 30 days, and audit metadata is retained for 365 days. Exempt unresolved
operations from pruning; show storage use and provide an explicit owner cleanup action.

Never prune stream watermarks, dedupe receipts needed for retained operations, active
bindings, unresolved commands or board-operation receipts merely because raw events
expire. Retained relationships must not dangle when text is pruned. Keep compact session/
run tombstones for historical associations. Expired tokens remain recognizable as
revoked/expired identities for audit, without usable secrets.

Back up auth.db, work.db, board files and referenced media using a consistent SQLite
backup procedure (not copying live WAL database files alone). Test restore and migration
failure. An older restore may lose events that collectors already discarded and may
resurrect credentials revoked after the backup: recovery must explicitly revoke/reissue
credentials, reconcile board receipts and reset producer streams before resuming live
ingest. Do not present backup recovery as lossless replay. Keep local JSONL during the
initial collector cutover until reconciliation and backup restore are verified.

### Transcript capture

Collectors forward a reduced transcript, not the whole conversation:

| Forwarded | Not forwarded |
|---|---|
| User prompts and assistant replies (text) | Tool output: file contents, command output, search results |
| One line per tool call: tool name plus the shell command or the file path acted on (bounded length) | Tool inputs beyond that line: file bodies written, patch text, binary content |

The tool-call lines make "which run touched this file" and "where was this command
run" searchable without storing bulk output, which is also where secrets most often
appear. A command line can still contain a secret typed inline, so collectors apply
their redaction/exclusion rules to tool-call lines as well as to text. Summaries
contain structured executable/action and affected paths, not raw shell payloads:
omit heredoc bodies, inline scripts, patch bodies and payload-bearing arguments such
as `printf` document contents. Use a tool-name-only fallback when parsing is uncertain.
Test these cases explicitly; length caps alone do not exclude written file bodies.

The full transcript stays on the machine that ran the agent. The card timeline offers
"open full transcript" through a separate bounded, paginated `transcript.read`
operation keyed by immutable collector/session/transcript ID, independent of the
current pane/run. The collector resolves only registered transcript references, never
arbitrary caller-supplied paths. It distinguishes offline, missing and deleted data.
V1 retrieval requires an owner session; reader tokens do not gain this permission.

Transcript bytes use a transient response path: no command-result bodies, audit
payloads, log capture, indexing or disk cache on the server. Persist only request
metadata and delivery status. Disconnects require a fresh fetch, not result replay.
Capture can be reduced to none per collector or per project. Verify retrieval after
pane reuse and confirm that no full-transcript bytes reach durable server stores.

## Build order and acceptance gates

1. **Shared auth and migration.** Inventory every route/tool; central principal/scope
   checks, one login, auth.db, Access settings, reader tokens and collector pairing.
   Verify cookie login across notes/kanban/Work, public-note isolation, 401/403 behavior,
   CSRF, invalid-bearer handling, cross-collector denial, expiry/revocation, pairing
   denial/expiry/code reuse, concurrent exchange, token redaction and legacy migration.
   Existing kanban and MCP integration checks must pass under the intended new scopes.
2. **One complete read/review path.** work.db, versioned binding, stream/ingest,
   heartbeat, one amythest-collector adapter, media, card Work tab, board pills and owner Review.
   Acceptance: bind → start run → produce output → disconnect → reconnect/replay →
   see output on the original card → review → new output becomes new again.
   Include sequence gaps/disorder, duplicate replay, malformed event recovery, restart,
   queue reset, rotation, commit/response crash boundaries, pane reuse, offline rebind,
   late artifact after finish, multi-card artifact identity and prune-then-replay tests.
3. **Collector cutover and search.** Shadow-send alongside local JSONL, reconcile counts
   and associations, then switch. Historical backfill must not trigger live alerts or
   commands. Add FTS, merged UI/MCP search; verify scope isolation, ranking determinism,
   archive/card filtering, late enrichment and retention/context behavior.
4. **Needs you and card write operations.** Snooze/connectivity/stall behavior, manual
   filing/attach, recoverable `work_done`, and human-only Done enforcement across every
   existing mutation path. Crash-test each board-operation boundary and concurrent human
   edits; test review/output races and archived-card late output.
5. **Commands, then approval.** Implement prompt/read with durable collector execution
   receipts. Verify lease expiry, lost responses, crash-to-unknown behavior, stale target,
   pending-approval prompt rejection and revocation. Enable approval only for adapters
   that pass exact-request identity/atomic-response tests; otherwise retain Open.
6. **Additional views and collectors.** Project page, Unfiled tray, optional daily note
   export; Pi/Hermes plugin and ROS collectors use the same contracts. Verify storage
   quotas, media eviction/overflow failure, backup/restore and migration rollback safety
   before declaring the module ready for routine use.
7. **Optional OAuth MCP interoperability.** Standards-based browser Connect for clients
   without configurable bearer headers; reuse the principal/scope model and test against
   representative clients. This does not block the v1 reader-token workflow.

## Decisions settled for v1

- Work is optional; authentication is application-wide and private by default.
- Collectors and MCP agents have separate registered identities and scoped tokens.
- Browser-assisted pairing and manually configured bearer readers are supported;
  arbitrary OAuth MCP client onboarding is a later explicit milestone.
- Binding history belongs to Work and is backed up; herdr labels are hints.
- Human-only Done and approval are enforced at the service boundary.
- Approval from phone and desktop follows the same identity/precondition checks;
  device type does not grant extra authority.
- Transcript capture defaults to user/assistant text plus one line per tool call
  (command or file path), never tool output, with project/collector opt-out. Full
  transcripts stay on the agent's machine and are fetched on demand.

### Implemented follow-up: session project assignment (2026-11-16)

Work's project gallery now lists sessions and supports moving a session to an
existing or new project. The owner assignment applies to all past and future
runs in that session, including project-scoped search and artifact views. The
collector's reported project remains recorded as provenance, and “Use collector
project” removes the override. Assignments use a version check and request receipt
so stale tabs cannot overwrite newer changes and retries are safe. Reader and
collector bearer tokens cannot change these owner assignments.

The forward migration from Work schema 1 to 2 adds only a session assignment table
and effective-project views. Before rolling back to an older binary, restore the
pre-upgrade Work database backup as well.

Amythest Collector can now opt individual Codex desktop chats into metadata-only lifecycle
reporting from their local saved lifecycle records. This also covers an already
running chat. See Amythest Collector's `deploy/watch-codex.py`; no conversation or tool content
is forwarded by that adapter. Herdr and Hermes collection remain enabled.
