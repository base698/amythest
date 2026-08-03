---
name: kanban
description: Read and update the Amythest Kanban boards backing the notes vault (kanban/<board>/board.md). Use whenever a task involves kanban cards, board columns, triage/backlog/ready/in-progress/verify/done status, card comments or attachments, or requests like "what's on my board", "add a card", "move this to verify", "what did I finish".
---

# Amythest Kanban

Boards are Markdown notes in the vault (`kanban/<board>/board.md` + `done.md`) served by
the Amythest server at `<base-url>/kanban`. **Always go through the API, never edit
`board.md` by hand** — writes need the per-board lock, atomic rename, and archive journal
that the server owns.

Use the CLI wrapper; it handles login, session caching, and CSRF:

```bash
python3 .claude/skills/kanban/scripts/kanban.py <command> [args]
```

Configuration (env vars):

- `KANBAN_BASE_URL` — URL of the kanban mount, default `http://127.0.0.1:8639/kanban`.
  Include any public path prefix, e.g. `https://host.example.com/notes/kanban`.
- `KANBAN_USERNAME` / `KANBAN_PASSWORD` — credentials (the same values the server reads
  at startup), else the first readable env file: `$KANBAN_ENV_FILE` or
  `~/.config/amythest/env`.
- The session is cached in `~/.cache/amythest-kanban/session.json`
  (`KANBAN_SESSION_FILE` overrides) and re-established automatically when it expires.

## Statuses

In flow order: `triage` → `backlog` → `ready` → `in_progress` → `verify` → `done`.
Moving to `done` archives the card out of `board.md` into `done.md`. Blocked is a flag,
not a column — a blocked card keeps its status.

## Commands

Reading:

```bash
kanban.py boards                                  # all boards + per-status counts
kanban.py board <board>                           # compact listing, grouped by status
kanban.py board <board> --status ready            # one column
kanban.py board <board> --json                    # full JSON (descriptions, comments, audit)
kanban.py card <board> <cardId>                   # one card as JSON
kanban.py search "text" [--board <board>] [--archived]
kanban.py archive <board> [--query text] [--limit 50]   # completed cards
```

Writing:

```bash
kanban.py create <board> --title "Fix X" --description "..." [--status triage] \
  [--assignee name] [--label infra --label urgent]
kanban.py create <board> --title "Fix X" --description-file notes.md   # or - for stdin
kanban.py update <board> <cardId> --status verify --title "..." --labels "a,b"
kanban.py move <board> <cardId> --status done          # --before <cardId> to order within a column
kanban.py comment <board> <cardId> --body "shipped in 3da9ab2"
kanban.py transfer <board> <cardId> --to <otherBoard>  # cross-board, moves attachments too
kanban.py delete <board> <cardId>                      # permanent, no archive
kanban.py restore <board> <cardId> --status backlog    # pull a done card back
kanban.py new-board <name>                             # lowercase/digits/hyphens
kanban.py settings <board> --dispatch on|off           # toggle the board's dispatchEnabled flag
```

Attachments (stored under `kanban/<board>/attachments/<cardId>/` in the vault):

```bash
kanban.py attach <board> <cardId> ./diagram.png   # max 10 MiB, 10 per card
kanban.py attachments <board> <cardId>
kanban.py download <board> <cardId> <attachmentId> [-o out.png]
kanban.py detach <board> <cardId> <attachmentId>
```

Escape hatch:

```bash
kanban.py raw GET /api/boards                     # any endpoint, --data '<json>' for a body
```

## Rules the server enforces

- Title 1–200 chars, single line. Description ≤ 10000 chars. Assignee ≤ 80 chars.
- ≤ 10 labels, each 1–32 safe chars (normalized lowercase). `--labels` replaces the whole set.
- Comments 1–2000 chars; author is the API user, not a flag.
- `--before` only orders within the same status, and is rejected when moving to `done`.
- Board names match `^[a-z0-9][a-z0-9-]{0,63}$`. There is no delete-board endpoint.
- Unknown JSON fields are rejected — when using `raw`, send exactly the documented keys.

## Watch out

- If the vault is a git repo, card writes show up as modified `board.md`/`done.md`. Leave
  committing to the user unless asked.
- Reading `board.md` directly is fine for a quick look (JSON lives between the
  `AMYTHEST_KANBAN_DATA_START/END` markers), but all mutations go through the API.
- A board directory without a `board.md` is not a board; don't create board dirs by hand.

## Source

Routes: `internal/kanban/httpapi/server.go`. Storage model: `internal/kanban/board/`.
The wire contract is guarded by `make compat`, which drives this client through the full
card lifecycle against a temp server (`scripts/compat.sh`).
