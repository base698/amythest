// Package index owns the SQLite database derived from the vault: note
// metadata, the link graph, tags, tasks, inline fields, the FTS5 search
// corpus, and the rendered-HTML cache. Everything here is disposable —
// deleting the database only costs a reindex.
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS notes(
  slug       TEXT PRIMARY KEY,
  path       TEXT NOT NULL,
  title      TEXT NOT NULL,
  frontmatter TEXT NOT NULL DEFAULT '{}',  -- JSON; {"_raw": "..."} when unparseable
  tags       TEXT NOT NULL DEFAULT '[]',   -- JSON array, frontmatter + inline
  aliases    TEXT NOT NULL DEFAULT '[]',
  mtime      INTEGER NOT NULL,
  ctime      INTEGER NOT NULL DEFAULT 0,  -- frontmatter created date, else mtime
  hash       TEXT NOT NULL,
  wordcount  INTEGER NOT NULL DEFAULT 0,
  archived        INTEGER NOT NULL DEFAULT 0,  -- 1 when frontmatter or tags mark the note archived
  archived_reason TEXT NOT NULL DEFAULT ''     -- provenance, e.g. "frontmatter:status" or "tag:archive"
);
CREATE INDEX IF NOT EXISTS notes_archived ON notes(archived);
CREATE TABLE IF NOT EXISTS links(
  from_slug TEXT NOT NULL,
  to_slug   TEXT NOT NULL,
  kind      TEXT NOT NULL DEFAULT 'link',  -- link|embed
  PRIMARY KEY(from_slug, to_slug, kind)
);
CREATE INDEX IF NOT EXISTS links_to ON links(to_slug);
CREATE TABLE IF NOT EXISTS tags(
  tag  TEXT NOT NULL,
  slug TEXT NOT NULL,
  PRIMARY KEY(tag, slug)
);
CREATE INDEX IF NOT EXISTS tags_slug ON tags(slug);
CREATE TABLE IF NOT EXISTS tasks(
  id         INTEGER PRIMARY KEY,
  slug       TEXT NOT NULL,
  line       INTEGER NOT NULL,
  text       TEXT NOT NULL,
  status     TEXT NOT NULL,               -- open|done|cancelled|other
  due        TEXT,
  scheduled  TEXT,
  start      TEXT,
  recurrence TEXT,
  priority   INTEGER NOT NULL DEFAULT 3,  -- 1 high … 5 low
  done_date  TEXT,
  tags       TEXT NOT NULL DEFAULT '[]',
  version    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_due ON tasks(status, due);
CREATE INDEX IF NOT EXISTS tasks_slug ON tasks(slug);
CREATE TABLE IF NOT EXISTS inline_fields(
  slug  TEXT NOT NULL,
  line  INTEGER NOT NULL,
  key   TEXT NOT NULL,
  value TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS inline_fields_key ON inline_fields(key);
CREATE INDEX IF NOT EXISTS inline_fields_slug ON inline_fields(slug);
CREATE TABLE IF NOT EXISTS list_items(
  slug   TEXT NOT NULL,
  line   INTEGER NOT NULL,
  text   TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT ''  -- '' plain bullet, else checkbox status
);
CREATE INDEX IF NOT EXISTS list_items_slug ON list_items(slug);
CREATE TABLE IF NOT EXISTS render_cache(
  slug        TEXT PRIMARY KEY,
  hash        TEXT NOT NULL,   -- note hash + dependency fingerprint
  html        BLOB NOT NULL,
  toc         TEXT NOT NULL DEFAULT '[]',
  rendered_at INTEGER NOT NULL
);
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
  slug UNINDEXED, title, content, tags,
  tokenize='unicode61 remove_diacritics 2'
);
`

// schemaVersion invalidates the whole database when the DDL changes.
const schemaVersion = 5

const dsnOpts = "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=mmap_size(268435456)"

// Open opens (creating if needed) the index database in dataDir. Writes go
// through a dedicated single connection; reads use a small pool so page
// loads never queue behind a reindex transaction (WAL allows concurrent
// readers).
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "index.db")

	w, err := sql.Open("sqlite", path+dsnOpts)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)

	// Everything here is derived data, so migrations are just "start over":
	// on any schema change, drop and reindex.
	var version int
	_ = w.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != schemaVersion {
		for _, t := range []string{"notes", "links", "tags", "tasks", "inline_fields", "list_items", "render_cache", "notes_fts"} {
			if _, err := w.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
				w.Close()
				return nil, err
			}
		}
		if _, err := w.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
			w.Close()
			return nil, err
		}
	}
	if _, err := w.Exec(schema); err != nil {
		w.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	r, err := sql.Open("sqlite", path+dsnOpts)
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	return &DB{r: r, w: w}, nil
}

type DB struct {
	r *sql.DB // read pool
	w *sql.DB // single write connection
}

func (d *DB) Close() error {
	rerr := d.r.Close()
	werr := d.w.Close()
	if rerr != nil {
		return rerr
	}
	return werr
}
