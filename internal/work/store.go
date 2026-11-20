// Package work stores durable agent activity separately from the rebuildable vault index.
package work

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Config struct {
	Enabled       bool          `yaml:"enabled"`
	DB            string        `yaml:"db"`
	MediaDir      string        `yaml:"media_dir"`
	MediaMaxFile  int64         `yaml:"media_max_file"`
	MediaMaxTotal int64         `yaml:"media_max_total"`
	AsleepAfter   time.Duration `yaml:"asleep_after"`
	StallAfter    time.Duration `yaml:"stall_after"`
}
type Store struct {
	mediaMu sync.Mutex
	db      *sql.DB
	cfg     Config
	now     func() time.Time
}
type Fault struct {
	Status   int    `json:"-"`
	Message  string `json:"error"`
	Acked    int64  `json:"acked"`
	Expected int64  `json:"expected_seq,omitempty"`
	Seq      int64  `json:"seq,omitempty"`
}

func (e *Fault) Error() string   { return e.Message }
func invalid(msg string) error   { return &Fault{Status: 422, Message: msg} }
func conflict(msg string) error  { return &Fault{Status: 409, Message: msg} }
func id() string                 { return uuid.NewString() }
func nowText(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func validID(s string) bool      { return len(s) > 0 && len(s) <= 160 }
func Open(cfg Config) (*Store, error) {
	if cfg.DB == "" || cfg.MediaDir == "" {
		return nil, errors.New("work database and media directory required")
	}
	if cfg.MediaMaxFile == 0 {
		cfg.MediaMaxFile = 50 << 20
	}
	if cfg.MediaMaxTotal == 0 {
		cfg.MediaMaxTotal = 20 << 30
	}
	if cfg.AsleepAfter == 0 {
		cfg.AsleepAfter = 3 * time.Minute
	}
	if cfg.StallAfter == 0 {
		cfg.StallAfter = 45 * time.Minute
	}
	if cfg.MediaMaxFile < 1 || cfg.MediaMaxTotal < cfg.MediaMaxFile || cfg.AsleepAfter <= 0 || cfg.StallAfter <= 0 {
		return nil, errors.New("invalid work limits")
	}
	for _, d := range []string{filepath.Dir(cfg.DB), cfg.MediaDir} {
		if e := os.MkdirAll(d, 0700); e != nil {
			return nil, e
		}
	}
	f, e := os.OpenFile(cfg.DB, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return nil, e
	}
	f.Close()
	db, e := sql.Open("sqlite", cfg.DB)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	if _, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); e != nil {
		return fail(e)
	}
	var version int
	if e = db.QueryRow(`PRAGMA user_version`).Scan(&version); e != nil {
		return fail(e)
	}
	if version > 4 {
		return fail(errors.New("work database newer than this binary"))
	}
	if version == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(schema); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if version < 2 {
		if _, e = db.Exec(projectSchema); e != nil {
			return fail(e)
		}
	}
	if version < 3 {
		if _, e = db.Exec(milestoneSchema); e != nil {
			return fail(e)
		}
	}
	if version < 4 {
		if _, e = db.Exec(milestoneLinkSchema); e != nil {
			return fail(e)
		}
	}
	return &Store{db: db, cfg: cfg, now: time.Now}, nil
}
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE machines(collector TEXT PRIMARY KEY,name TEXT NOT NULL,last_contact TEXT NOT NULL,syncing INTEGER NOT NULL DEFAULT 1);
CREATE TABLE streams(collector TEXT NOT NULL,generation TEXT NOT NULL,acked INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL CHECK(state IN('active','closed')),PRIMARY KEY(collector,generation));
CREATE UNIQUE INDEX active_stream ON streams(collector) WHERE state='active';
CREATE TABLE slots(collector TEXT NOT NULL,pane TEXT NOT NULL,session_id TEXT NOT NULL,status TEXT NOT NULL,project TEXT NOT NULL,tab TEXT NOT NULL,last_output TEXT NOT NULL,inactivity INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(collector,pane));
CREATE TABLE sessions(id TEXT PRIMARY KEY,collector TEXT NOT NULL,pane TEXT NOT NULL,provider_ref TEXT NOT NULL,agent TEXT NOT NULL,project TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE bindings(id TEXT PRIMARY KEY,collector TEXT NOT NULL,pane TEXT NOT NULL,session_id TEXT NOT NULL REFERENCES sessions(id),version INTEGER NOT NULL,card_id TEXT NOT NULL,active INTEGER NOT NULL,acked INTEGER NOT NULL DEFAULT 0,created TEXT NOT NULL);
CREATE UNIQUE INDEX current_binding ON bindings(collector,pane) WHERE active=1;
CREATE UNIQUE INDEX binding_version ON bindings(collector,pane,version);
CREATE TABLE runs(id TEXT PRIMARY KEY,collector TEXT NOT NULL,session_id TEXT NOT NULL REFERENCES sessions(id),binding_id TEXT NOT NULL, binding_version INTEGER NOT NULL,card_id TEXT NOT NULL,project TEXT NOT NULL,started TEXT NOT NULL,finished TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 0,reviewed INTEGER NOT NULL DEFAULT 0,historical INTEGER NOT NULL DEFAULT 0);
CREATE INDEX card_runs ON runs(card_id,started);
CREATE UNIQUE INDEX one_active_run ON runs(session_id) WHERE finished='' AND historical=0;
CREATE TABLE events(collector TEXT NOT NULL,generation TEXT NOT NULL,seq INTEGER NOT NULL,received TEXT NOT NULL,occurred TEXT NOT NULL,kind TEXT NOT NULL,payload TEXT NOT NULL,hash TEXT NOT NULL,PRIMARY KEY(collector,generation,seq));
CREATE TABLE artifacts(id TEXT PRIMARY KEY,identity_key TEXT UNIQUE NOT NULL,kind TEXT NOT NULL,ref TEXT NOT NULL,title TEXT NOT NULL,repo TEXT NOT NULL,media_hash TEXT NOT NULL DEFAULT '',remote_path TEXT NOT NULL DEFAULT '',created TEXT NOT NULL);
CREATE TABLE links(id TEXT PRIMARY KEY,artifact_id TEXT NOT NULL REFERENCES artifacts(id),run_id TEXT NOT NULL REFERENCES runs(id),card_id TEXT NOT NULL,attribution TEXT NOT NULL,featured INTEGER NOT NULL DEFAULT 0,filed_by TEXT NOT NULL DEFAULT '',UNIQUE(artifact_id,run_id));
CREATE TABLE documents(key TEXT PRIMARY KEY,kind TEXT NOT NULL,title TEXT NOT NULL,body TEXT NOT NULL,run_id TEXT NOT NULL REFERENCES runs(id),created TEXT NOT NULL,version INTEGER NOT NULL,artifact_id TEXT NOT NULL DEFAULT '');
CREATE VIRTUAL TABLE work_fts USING fts5(key UNINDEXED,title,body,tokenize='porter unicode61 remove_diacritics 2');
CREATE TABLE media(hash TEXT PRIMARY KEY,bytes INTEGER NOT NULL,content_type TEXT NOT NULL,created TEXT NOT NULL,available INTEGER NOT NULL DEFAULT 1);
CREATE TABLE media_owners(hash TEXT NOT NULL REFERENCES media(hash),collector TEXT NOT NULL,PRIMARY KEY(hash,collector));
CREATE TABLE receipts(actor TEXT NOT NULL,request_id TEXT NOT NULL,hash TEXT NOT NULL,result TEXT NOT NULL,PRIMARY KEY(actor,request_id));
CREATE TABLE snoozes(subject TEXT PRIMARY KEY,version TEXT NOT NULL,until_at TEXT NOT NULL);
CREATE TABLE audit(id INTEGER PRIMARY KEY,actor TEXT NOT NULL,action TEXT NOT NULL,subject TEXT NOT NULL,at TEXT NOT NULL);
PRAGMA user_version=1;
`

type Binding struct {
	ID           string `json:"id"`
	Collector    string `json:"collector"`
	Pane         string `json:"pane"`
	Session      string `json:"session_id"`
	Version      int64  `json:"version"`
	Card         string `json:"card_id"`
	Acknowledged bool   `json:"acknowledged"`
}
type Run struct {
	Reviewable bool   `json:"reviewable"`
	ID         string `json:"id"`
	Collector  string `json:"collector"`
	Session    string `json:"session_id"`
	Card       string `json:"card_id"`
	Project    string `json:"project"`
	Started    string `json:"started_at"`
	Finished   string `json:"finished_at"`
	Outcome    string `json:"outcome"`
	Version    int64  `json:"output_version"`
	Reviewed   int64  `json:"reviewed_through_version"`
	Historical bool   `json:"historical"`
}
type Artifact struct {
	// Input attachments use the reserved input: identity namespace. The flag is
	// derived on read so existing stored artifacts and collectors stay compatible.
	Input       bool   `json:"input,omitempty"`
	ID          string `json:"id"`
	Key         string `json:"identity_key"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Repo        string `json:"repo"`
	MediaHash   string `json:"media_hash"`
	RemotePath  string `json:"remote_path"`
	LinkID      string `json:"link_id"`
	RunID       string `json:"run_id"`
	Card        string `json:"card_id"`
	Attribution string `json:"attribution"`
	Featured    bool   `json:"featured"`
	Available   bool   `json:"available"`
	Created     string `json:"created_at"`
}
type Document struct {
	ArtifactID string `json:"artifact_id,omitempty"`
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Run        string `json:"run_id"`
	Created    string `json:"created_at"`
	Version    int64  `json:"output_version"`
}
type Slot struct {
	BindingVersion int64    `json:"binding_version"`
	Collector      string   `json:"collector"`
	Pane           string   `json:"pane"`
	Session        string   `json:"session_id"`
	Status         string   `json:"status"`
	Project        string   `json:"project"`
	Tab            string   `json:"tab"`
	LastOutput     string   `json:"last_output_at"`
	Inactivity     int64    `json:"inactivity_seconds"`
	Binding        *Binding `json:"binding,omitempty"`
}
type Machine struct {
	ID          string `json:"collector"`
	Name        string `json:"name"`
	LastContact string `json:"last_contact"`
	Syncing     bool   `json:"syncing"`
	Online      bool   `json:"online"`
}
type CardData struct {
	Projects  []string         `json:"projects"`
	Sessions  []SessionProject `json:"sessions"`
	Runs      []Run            `json:"runs"`
	Artifacts []Artifact       `json:"artifacts"`
	Documents []Document       `json:"documents"`
	Slots     []Slot           `json:"slots"`
	Machines  []Machine        `json:"machines"`
}

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	e := row.Scan(&r.ID, &r.Collector, &r.Session, &r.Card, &r.Project, &r.Started, &r.Finished, &r.Outcome, &r.Version, &r.Reviewed, &r.Historical)
	return r, e
}

const runColumns = `id,collector,session_id,card_id,project,started,finished,outcome,version,reviewed,historical`

func receipt(tx *sql.Tx, actor, request, hash string, out any) (bool, error) {
	if !validID(request) {
		return false, invalid("request_id required")
	}
	var h, b string
	e := tx.QueryRow(`SELECT hash,result FROM receipts WHERE actor=? AND request_id=?`, actor, request).Scan(&h, &b)
	if e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if h != hash {
		return false, conflict("request_id reused with different input")
	}
	return true, json.Unmarshal([]byte(b), out)
}
func saveReceipt(tx *sql.Tx, actor, request, hash string, result any) error {
	b, e := json.Marshal(result)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO receipts VALUES(?,?,?,?)`, actor, request, hash, string(b))
	return e
}
func (s *Store) Ping() error { return s.db.Ping() }
func wrapSeq(err error, acked, seq int64) error {
	var f *Fault
	if errors.As(err, &f) {
		f.Acked = acked
		f.Seq = seq
		return f
	}
	return fmt.Errorf("event %d: %w", seq, err)
}
