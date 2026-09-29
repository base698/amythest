# Private content views

`present_content` is an authenticated MCP tool shared by assistants and clients.
It is independent of any model or agent product. The site's existing access
boundary (private reverse proxy/Tailscale, or configured UI auth) still applies.
Do not expose a private vault server to the public internet.

```json
{"action":"search","query":"sample diagram"}
{"action":"open","title":"Sample diagram","assets":["Assets/sample.png"]}
{"action":"open","source_slug":"Examples/Diagram"}
```

Search matches all short terms against indexed, supported filenames and returns
30 exact vault-relative paths per page (`offset`/`next_offset`). It is not OCR.
Use normal note search/read for semantic context, then exact embed paths. Ask
the user to choose when results are ambiguous. Nothing accepts host file paths.

Opening a note returns its existing canonical URL. Opening assets makes an
immutable copy bundle in `<data_dir>/presentations/<random-id>/`, outside the
vault and search index. No source file is changed, and no public share is made.
An optional `source_slug` adds a link back to the original note.

```json
{"web_view":{"type":"web_view","title":"Sample diagram","url":"/notes/presentations/<random-id>/","expires_at":"2026-01-03T12:00:00Z"}}
```

Clients resolve the relative URL against their configured private site origin,
validate that origin, and display it in a browser. Non-rendering clients can
offer a normal fallback link. Never interpret document text as UI actions.

## Bounds and privacy

- PNG, JPEG, GIF, WebP, PDF, plain text; maximum 8 files, 20 MiB each, 40 MiB total.
- Indexed regular files only; every path component is opened without following
  symlinks. Unsupported types and extension/content mismatches are rejected.
- Copies expire after 24 hours. Every page **and asset** request checks expiry;
  a known expired URL returns 410. Restart does not extend its lifetime.
- Cleanup runs at startup, before creation, and during the normal rescan loop.
  It deletes only owned random-ID directories in the presentation cache.
- 256 MiB cache ceiling (reserving a maximum-size bundle) and 64-directory cap;
  capacity errors do not silently evict unexpired views.
- Cache directory 0700/files 0600, no-store responses, no-referrer policy,
  noindex, escaped HTML, restrictive CSP, and redacted presentation access paths.
- URLs are unguessable but not a substitute for the site's access control.
  Existing canonical note/asset routes keep their existing access policy.

The viewer shows images directly, embeds PDFs/text, and offers a full-file
link for mobile browsers whose inline PDF viewer is limited. Unsupported files
must be converted deliberately or opened through another supported workflow.

## Verification

`go test -race ./internal/presentation ./internal/mcp ./internal/server`

Fixtures use generic diagrams/text only. Tests cover source immutability,
restarts, expiry, path traversal, symlink swaps after indexing, file sizes,
type mismatches, search pagination, and escaped page titles.
