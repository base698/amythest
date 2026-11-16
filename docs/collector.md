# Amythest Collector

The collector shipped in the Amythest repository, built as `cmd/amythest-collector`. The existing `watch`, `collect`, `panes`, `hook`
and `show` prototype commands still work. `sync` is an opt-in shadow sender for the
local herdr TUI; remote SSH and herdr-core remain available through the older watcher,
but are not yet hub-delivery adapters.

```sh
make build-collector
./bin/amythest-collector register --server https://notes.example.com/notes --name laptop
./bin/amythest-collector sync
```

Work must be enabled on Amythest. Registration opens a browser approval link and saves
`~/.local/state/amythest-collector/credential.json` with mode 0600. `AMYTHEST_COLLECTOR_STATE` changes the state
directory; `AMYTHEST_COLLECTOR_HERDR_SOCK` selects a different local herdr socket. `--credential`
selects another credential file. `sync --once` only drains an existing queue.

The queue (`hub.json`, mode 0600) atomically persists generation, sequence, session/run
IDs, binding snapshots, pending artifact scans and events. Writes fsync the temporary
file and containing directory; a process lock prevents concurrent producers. Only an
acknowledged prefix is removed. Failed delivery leaves the queue intact, and a changed
server/client identity is rejected. Replacement credentials must preserve `client_id`;
restart sync after updating the file, then revoke the old token in Access settings.

Assign a current agent to a card using the card's Work tab. Assignments affect future
runs. An observed working transition starts a run; done/idle ends it. Disconnects do
not end runs. Confirmed pane loss abandons a run. Provider-session changes create a
new incarnation. If the adapter cannot provide a session reference, a restart treats
its previous identity as unknown rather than assuming a pane was not reused.

Run completion schedules artifact discovery. Results can arrive after finish and
reopen review. Repository scans are suggestions, not proof that a particular agent
created the output; accept them explicitly in Work. File observations get durable IDs;
commits use canonical remote/repository identity plus SHA where available. Bytes stay
on the collector in this first sender: automatic media upload and reduced transcript
capture/redaction are still to be implemented. The existing transcript detector only
extracts artifact references; it does not forward conversation text.

Existing local JSONL records are retained. Running both `watch` and `sync` can append
repeat local discoveries; reconcile by source identity before cutover. Do not delete
or hand-edit `hub.json` to bypass a rejected event. Invalid batches require repair or
owner quarantine of exactly the next sequence. A lost queue requires an explicit
server-side generation reset and a deliberate recovery procedure. Full backfill and
backup/restore reconciliation are not yet implemented.

Queue capacity is 100,000 events. Capture stops visibly on persistence/capacity errors;
it never auto-skips. This initial atomic-file implementation favors recoverability at
small volumes; a larger deployment needs queue storage and throughput measurement.
The adapter does not report guessed output inactivity or enable terminal commands.

Validation: `go test -race ./...`. Queue tests cover restart, exclusive ownership,
acknowledgement bounds, immutable run bindings, late discovery and pane reuse.

## Lifecycle hooks and services

Herdr supplies agent lifecycle events via its local socket. Install the native
integrations for agents you use (`herdr integration install claude`, and `... hermes`)
so status and provider session IDs are authoritative. Amythest Collector subscribes before taking
its live snapshot so startup does not leave a gap in event capture.

Hermes also uses the bundled `integrations/hermes/amythest-lifecycle` backend plugin.
Copy it to `~/.hermes/plugins/amythest-lifecycle`, then enable it with `hermes plugins enable amythest-lifecycle`. It spools metadata-only
start/end/approval/session-finalize facts to `$AMYTHEST_COLLECTOR_STATE/hooks` (default
`~/.local/state/amythest-collector/hooks`). Start and end use exact provider session/turn IDs;
`on_session_end` is a turn boundary, and `on_session_finalize` closes the session.
Prompt text, replies, commands, tool arguments/results and credentials are excluded.
Set `AMYTHEST_COLLECTOR_LIFECYCLE=0` to disable capture for a process. `AMYTHEST_COLLECTOR_PROJECT` supplies an
optional label; `AMYTHEST_COLLECTOR_WORKDIR` explicitly enables discovery in a chosen directory.

Amythest Collector drains the spool transactionally with replay receipts. Direct hooks take over
an observed Hermes run without duplicating it; later screen-idle reports cannot end
it early. Late callbacks from an old turn cannot finish a newer turn. The gateway and
interactive Hermes processes both load plugins at startup; restart/resume existing
interactive sessions to activate a newly installed plugin.

Install the sender as a user service after registering its credential:

```sh
python3 deploy/install-collector.py --binary /path/to/amythest-collector
# On a Mac displaying SSH-remote Hermes panes, collect those on their actual host:
python3 deploy/install-collector.py --binary /path/to/amythest-collector --skip-hermes-panes
```

`sync --capture-only` captures while the server is unavailable or not yet configured.
It never discards the durable queue. Switch the service to normal sync when registered.
The installer saves old binaries/units, preserves unrelated watcher services, and uses
launchd on macOS or a systemd user service on Linux. Check `amythest-collector-sync.log` or
`journalctl --user -u amythest-collector-sync` for delivery failures.

### Codex desktop sessions

Opt an existing chat into lifecycle reporting (the collector must already be
registered and `amythest-collector sync` running):

```sh
python3 deploy/watch-codex.py --rollout /absolute/path/to/rollout.jsonl \
  --session THREAD_ID --project Example --title 'Example session'
```

This reads only explicitly selected files from `~/.local/state/amythest-collector/codex.json`.
It projects saved `task_started`, `task_complete`, and `turn_aborted` records into
start, finish, and cancelled events. Prompts, responses, tool contents and working
files are not uploaded. Existing turns in that chat are included on first use;
checkpoints and durable receipts prevent duplicates after restart. It also works
for an already-running desktop chat, without restarting the app or changing hook
trust settings. Change project assignment later in Amythest Work → Projects →
Sessions; that assignment covers earlier and future runs.

This adapter is tested with Codex CLI/app-server 0.166 rollout records, which are
an internal format rather than a stable public API. It does not infer approval
waits or session closure from inactivity, and a hard app crash leaves the last
run unresolved until the next explicit lifecycle event. If a selected file moves
(e.g. archiving), rerun the installer with its new path; replay is deduplicated.
Remove its entry from `codex.json` to stop watching it. Native lifecycle producers
can use `agent: "codex"` in the same spool contract; omitted `agent` retains Hermes
compatibility.

## Migrating from the retired standalone collector

Use `python3 deploy/install-collector.py --binary /path/to/amythest-collector
--migrate-corral` (add `--skip-hermes-panes` on a machine displaying remote Hermes
panes). The installer stops the old sender before moving its state directory,
preserves the credential, stream and checkpoints, and installs the Amythest service.
A compatibility symlink allows already-running old lifecycle plugins to finish their
turns. Enable the bundled `amythest-lifecycle` Hermes plugin and disable the old
`corral-lifecycle` plugin. Restart interactive agents at a convenient boundary.
The old binary and service files are retained only in the migration backup.

## Artifact media uploads

Repository discovery now uploads supported image and video artifacts to the private
Work server. Lifecycle hooks still send metadata only; discovery runs only for an
observed repository or an explicitly configured work directory. Captured media is
shown inline in Work, with enlarge, new-tab and download controls.

The collector snapshots regular JPEG, PNG, GIF, WebP, MP4 and WebM files before
queuing their content hash. It rejects symlinks, mismatched content and files over
50 MiB. The private `media-outbox` holds up to 512 MiB of pending copies. Uploads
retry independently of acknowledged events and survive restarts; successful uploads
remove the local copy. Each sync attempts one file, with a one-minute timeout, so
lifecycle delivery continues during media failures. Missing, unsupported or oversized
files remain listed as being on the collector. Server media quotas still apply.


Commit artifacts also capture the exact commit's text diff into the same durable
outbox. Capture includes the file summary and first-parent diff for merges; external
diff commands and text conversion are disabled. Working-tree changes are excluded.
Captures are limited to 256 KiB with an explicit truncation notice; binary changes
are summarized rather than bundled. Private/local repositories remain viewable in
Work without publishing source code to a Git host.
