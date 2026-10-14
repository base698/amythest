# Operations acceptance checks

Run these checks against a test vault before deploying retrieval changes.

## Note properties

Run `bin/amy -endpoint https://notes.example.com/notes` with configured credentials.
Open a disposable note with frontmatter, press `p`, choose a property with `j`/`k`,
and press `Enter` or `e` to edit it. An empty value clears the property. Test
`tasks: false` on a disposable note. Notes without frontmatter have no properties
affordance. The web reader exposes a read-only Properties disclosure.

## Missing credentials

Run `KANBAN_ENV_FILE=/dev/null KANBAN_SESSION_FILE=/tmp/amy-missing-credential-test.json env -u KANBAN_USERNAME -u KANBAN_PASSWORD bin/amy -endpoint https://notes.example.com/notes`.

Expect a persistent setup-required panel with safe retry guidance. This does not
overwrite the regular session file. The configured `-check` must still succeed.

## Search

Use a synthetic vault containing dated notes, tasks and active/archived cards.
Check date filters, type filters, stemming, aliases, the Include archived toggle,
and distinct cards in the same board. Inspect `date_source`, `match_method` and
relative `url` in API/MCP results. Edits must refresh through the rescan pipeline.

Retrieval uses FTS5 Porter stemming, note aliases and explicit lexical associations;
it does not generate embeddings or claim semantic similarity. Run the Go suite,
web tests and production asset build before rollout.
