# Work: first read/review slice

Work is opt-in and requires `auth_mode: private`. This slice is ready for local and
shadow testing. It is not the complete module described in `work-module.md`; the
remaining cutover gates are listed below. The authentication deployment can keep
running with Work disabled.

```yaml
work:
  enabled: true
  # Defaults: <data_dir>/work.db and <data_dir>/work-media
  # db: ~/.local/state/amythest/work.db
  # media_dir: ~/.local/state/amythest/work-media
  media_max_file: 52428800       # bytes; 50 MiB
  media_max_total: 21474836480   # bytes; 20 GiB
  asleep_after: 3m
  stall_after: 45m
```

`work.db` uses SQLite WAL, FULL synchronous commits, foreign keys and a versioned
schema. It is never discarded when rebuilding the notes index. Back up auth.db,
work.db and media alongside the vault; copying a live database file without its WAL
is not a valid backup. SQLite's online backup API or a stopped-service copy is needed.
Restore drills and reconciliation remain a cutover gate.

## Using the first path

1. Register a collector with `amythest-collector register --server https://host/notes --name laptop`.
   Approve its code in Access settings. `amy auth login --preset collector` also works.
2. Run `amythest-collector sync` on the machine running the herdr TUI. It keeps a durable local
   queue and preserves local JSONL discoveries. Failed HTTP delivery never drops events.
3. Open an existing card's **Work** tab and assign a current agent to future runs.
   Pending assignment stays visible until the collector acknowledges it. Reassignment
   never changes the provenance of an already-running or finished run.
4. Start work in that agent. Runs and discovered output appear on the card. Repository
   scans provide suggestions; **File on this card** explicitly accepts an association.
5. Use **Review shown output** to acknowledge the displayed version. A later artifact
   or message makes the finished run need review again. This never moves the card.

Uploaded images appear inline anywhere artifacts are listed. Click an image or
**Enlarge** for a modal preview; **Open in new tab** and **Download** are available
under each image and inside its preview. Supported videos have playback controls.
Unavailable files remain labeled as being on the collector. Media requires the same
private authentication as Work.

Commit artifacts offer **View diff**, **Open diff in new tab**, and **Download
patch** when their captured diff is available. **Open on GitHub** links to the exact
commit when its recorded repository identity is on github.com. An unpushed commit
may not yet exist there; the captured diff remains available in Work. Large captured
diffs are explicitly marked as truncated, and the modal shows up to 4,000 lines.

The board displays Work badges. Focus has **Needs you**, with completed turns grouped
by session, card and outcome. Each entry identifies the session, project, collector
and completion time, and previews captured output. A successful turn only means the
agent finished that turn; it does not certify the task result. When no output was
captured, the UI says so. Expand **View turn details** to inspect output and mark it
reviewed, including output from runs outside the recent card window.

**Dismiss** hides the displayed notification versions permanently without reviewing
or deleting output. New output or changed state can bring the notification back.
The bulk action dismisses the loaded finished turns; blocked and offline alerts
remain. **Snooze 1h** hides the same versions temporarily. **Sessions, projects and
Work search** contains project assignment, an Unfiled tray and Work search. Machine connectivity is separate from run
outcome. Historical imports do not create review alerts. Collectors without a reliable
output clock do not claim inactivity or produce synthetic stall alerts.

Card views are bounded to 200 recent runs, 500 artifact associations and 200 reduced
messages. Review is disabled for a run whose retained output does not all fit in the
view. Attention includes up to 1,000 newest unmuted pending reviews; it is not limited to the
recent-run overview. Larger-history navigation is a remaining task.

## Milestones

A milestone marks a stretch of work on a project, such as getting production
deployed. Start it when the work begins and end it when it is done; while it is
open, everything the project produces belongs to it:

```sh
amy milestone start production deploy   # in the workspace (or -project NAME)
amy milestone                           # what is open here
amy milestone end                       # the only open one, or end NAME
amy milestone collect "production deploy" -json -o deploy.json
```

`collect` gathers the milestone's window: sessions and runs that overlap it,
artifacts and agent notes created in it, and messages put on the project's wall
during it (from Messages, when set up here). Without `-json` it prints a
readable report. Nothing is stamped as it arrives, so late events still land in
the right milestone. The wall's header shows open milestones (`◆ NAME`).

Milestones are kept by the work server (`/api/work/milestones`): reader tokens
can list and collect them, starting and ending need the owner.

## HTTP and MCP

Every collector request uses its bearer token; the server derives the immutable
collector ID from the credential. A browser cookie cannot impersonate a collector.

| Collector endpoint | Body / purpose |
|---|---|
| `POST /api/work/streams` | `{generation}` creates or resumes one active generation |
| `POST /api/work/events` | `{generation, events}`; at most 500 events / 2 MiB |
| `POST /api/work/heartbeat` | `{name, syncing, inactivity}`; inactivity maps session IDs to seconds |
| `GET /api/work/bindings` | Current assignments for this collector only |
| `POST /api/work/bindings/ack` | `{id, version}`; stale versions conflict |
| `PUT /api/work/media/{sha256}` | Raw bytes; verified hash, bounded size and quota |

Event kinds and binding snapshots follow `work-module.md`. A gap returns 409 with
`acked` and `expected_seq`; invalid data returns 422 with `seq` and `acked`. A failed
batch applies no new projections or acknowledgement. Replay ignores committed prefixes;
retained event content must match. Generation reset is owner-only. Never delete a queue
and silently restart numbering against the same generation.

Owner actions are CSRF-protected: `POST /api/work/bindings`, `/review`, `/snooze`, `/dismiss`,
`/file`, `/reset` and `/quarantine`. Binding and filing use `request_id` receipts;
binding also requires `expected_version`. Quarantine advances only the next sequence
and records the rejected position/reason. It does not repair dependent missing IDs.

Readers and owners can use `GET /api/work/snapshot?card_id=&project=`, `/inbox`,
`/search?q=&card_id=&project=&kind=&after=&before=&limit=` and `/media/{sha256}`.
Use `snapshot?summary=1` for the smaller overview and `snapshot?run_id=` to inspect
a specific run. `POST /api/work/dismiss` accepts `{items: [{subject, version}]}`
(up to 1,000); a stale version rejects the entire batch with 409. Search applies filters before ranking,
returns at most 100 hits and indexes a shared artifact once across associations.
Dates are inclusive YYYY-MM-DD. Refiling changes artifact search scope immediately.
The MCP reader exposes `work_card`, `work_inbox` and `work_search`. Readers cannot
review, assign, file output, ingest events or retrieve full transcripts.

Media metadata may precede bytes. Availability requires a matching upload from the
source collector. HTML/SVG and unknown formats download as opaque attachments;
only supported raster/video types can display inline. No URL or collector path is
fetched by the server. Uploads evict the oldest unfeatured blobs when necessary;
featured associations protect shared bytes. If protected bytes fill the quota,
uploads return 507 and artifact metadata stays visible.

Access settings' **Replace token** retains `client_id` while issuing a new credential
ID and secret. Switch the client's credential file, restart its process, and revoke the
old credential after verification. Both secrets work during the overlap. Registering
again creates a different collector identity and cannot take over an existing queue.

## Remaining implementation and cutover gates

- Amythest Collector automatic media upload and reduced transcript capture with tested redaction;
  immutable historical transcript retrieval; deterministic JSONL backfill/reconciliation.
- Merged notes/Work ranking, linked-note scopes, pagination and richer project/media views.
- Scheduled retention and orphan-media cleanup, storage controls, restore drills and
  injected crash tests at database/response/filesystem boundaries.
- Recoverable `work_done` through one canonical board transaction; agent writer/control
  scopes with shared human-only Done enforcement. Private MCP remains read-only.
- Durable commands and local execution receipts. No prompt, read, approve or deny
  controls are exposed by this slice. Approval requires an adapter with exact request
  identity and an atomic response, as specified in the plan.
- Additional adapters, optional daily exports and optional standards-based OAuth.

Tests cover batch atomicity, gaps, replay/prune/restart/reset, foreign ownership,
immutable bindings, late output and review races, shared artifact search/manual filing,
pending media, quotas/featured protection, token replacement and route capabilities.
Amythest Collector's queue has restart/locking/rebind/pane-reuse tests. A local HTTP smoke test also
ran the Amythest Collector binary against a simulated herdr socket and the actual Amythest service.
