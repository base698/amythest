# Operations acceptance — 14 October 2026

This branch includes Hermes's previously unpushed note-properties and connection-error commits, followed by unified lexical retrieval. The server is deployed on Netexplore; a Mac client is built in `bin/amy`.

## Note properties

Run `bin/amy -endpoint https://netexplore.tailc646a5.ts.net/notes` from this checkout. It uses your existing local credentials. Open a note with frontmatter, press `p`, select a property with `j`/`k`, and press `Enter` or `e` to edit it. An empty value clears the property. Test `tasks: false` on a disposable note, not a watchlist you still want excluded. A note without frontmatter has no properties affordance. The web note reader exposes a read-only Properties disclosure.

## Missing credentials

Run `KANBAN_ENV_FILE=/dev/null KANBAN_SESSION_FILE=/tmp/amy-missing-credential-test.json env -u KANBAN_USERNAME -u KANBAN_PASSWORD bin/amy -endpoint https://netexplore.tailc646a5.ts.net/notes`.

Expect a persistent setup-required panel naming the credential category and safe retry guidance. This command does not overwrite your regular session file. The regular configured `-check` must still succeed.

## Search

Open Notes, press Command-K, and try:

- `robotics after:2026-07-01 before:2026-07-31`
- `robotics type:card` (toggle Include archived to see completed cards)
- `walk`, `walking`, `vision`, and `camera`
- `after:2026-10-14 before:2026-10-14 type:task`

Results show type and source date; API/MCP also returns `date_source`, `match_method`, and the direct relative `url`. Card dates mean last card update. Note/task dates prefer frontmatter date/creation, then a filename date, then file modification. These dates are evidence about the source, not proof of when work occurred. Edits refresh through the existing rescan/index pipeline.

Myke/Hermes can pass the same short query syntax to `search_notes`. The existing broker forwards it unchanged. Myke now keeps distinct cards from the same board instead of collapsing them by note slug.

## Semantic scope

This release uses FTS5 Porter stemming, note aliases, and explicit related project terms. It does not generate embeddings or claim semantic similarity. Canon uses separately stored, model-specific dense embeddings with provenance; an optional Amythest embedding index remains separate work. Raw Agent Jobs output is discoverable through the existing jobs tool; only job output filed as notes enters this index.

## Validation

All Go tests, `go vet ./...`, 70 web tests, and the production web build passed. A separate loopback instance indexed all 1,888 live notes before rollout. Real July robotics and active/archived card searches passed. The Mac client authenticated and listed five boards.
