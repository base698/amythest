package work

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProjectAssignmentPersistsWithoutChangingProvenance(t *testing.T) {
	s := testStore(t)
	if _, e := s.Stream("collector", "generation"); e != nil {
		t.Fatal(e)
	}
	seen := event(1, "slot.seen")
	seen.Project = "Old"
	seen.Agent = "codex"
	seen.Tab = "Plan"
	start := event(2, "run.start")
	start.Project = "Old"
	chat := event(3, "chat")
	chat.DocumentID = "doc"
	chat.Role = "assistant"
	chat.Text = "durable metadata"
	ingest(t, s, seen, start, chat)
	if v, e := s.AssignProject("assign", "session", "Amythest", 0); e != nil || v != 1 {
		t.Fatal(v, e)
	}
	// Retry remains successful even after subsequent edits.
	if _, e := s.AssignProject("stale", "session", "Wrong", 0); e == nil {
		t.Fatal("stale assignment accepted")
	}
	finish := event(4, "run.finish")
	finish.Outcome = "success"
	seen.Seq = 5
	next := event(6, "run.start")
	next.Run = "next"
	next.Project = "Old"
	ingest(t, s, finish, seen, next)
	d, e := s.Snapshot("", "Amythest")
	if e != nil || len(d.Runs) != 2 || len(d.Documents) != 1 || len(d.Slots) != 1 || len(d.Sessions) != 1 {
		t.Fatal(d, e)
	}
	if d.Sessions[0].SourceProject != "Old" || !d.Sessions[0].Assigned {
		t.Fatal(d.Sessions)
	}
	old, e := s.Snapshot("", "Old")
	if e != nil || len(old.Runs) != 0 || len(old.Slots) != 0 || len(old.Documents) != 0 || len(old.Sessions) != 0 {
		t.Fatal(old, e)
	}
	hits, e := s.Search(SearchOptions{Query: "durable", Project: "Amythest"})
	if e != nil || len(hits) != 1 || hits[0].Project != "Amythest" {
		t.Fatal(hits, e)
	}
	hits, e = s.Search(SearchOptions{Query: "durable", Project: "Old"})
	if e != nil || len(hits) != 0 {
		t.Fatal(hits, e)
	}
	var original string
	if e = s.db.QueryRow(`SELECT project FROM runs WHERE id='run'`).Scan(&original); e != nil || original != "Old" {
		t.Fatal(original, e)
	}
	cfg := s.cfg
	s.Close()
	s, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e = s.Overview()
	if e != nil || d.Sessions[0].Project != "Amythest" {
		t.Fatal(d, e)
	}
	if v, e := s.AssignProject("reset", "session", "", 1); e != nil || v != 2 {
		t.Fatal(v, e)
	}
	if v, e := s.AssignProject("assign", "session", "Amythest", 0); e != nil || v != 1 {
		t.Fatal("receipt replay", v, e)
	}
	d, e = s.Snapshot("", "Old")
	if e != nil || len(d.Runs) != 2 || d.Sessions[0].Assigned || d.Sessions[0].Version != 2 {
		t.Fatal(d, e)
	}
	if _, e = s.AssignProject("invalid", "session", "bad\nproject", 2); e == nil {
		t.Fatal("control character accepted")
	}
}
func TestProjectMigrationFromVersionOne(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DB: filepath.Join(dir, "work.db"), MediaDir: filepath.Join(dir, "media")}
	db, e := sql.Open("sqlite", cfg.DB)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(schema); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO sessions VALUES('legacy','collector','pane','provider','hermes','Existing','2026-01-01')`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := s.Overview()
	if e != nil || len(d.Sessions) != 1 || d.Sessions[0].Project != "Existing" {
		t.Fatal(d, e)
	}
	if _, e = s.AssignProject("move", "legacy", "amythest", 0); e != nil {
		t.Fatal(e)
	}
	var v int
	if e = s.db.QueryRow(`PRAGMA user_version`).Scan(&v); e != nil || v != 4 {
		t.Fatal(v, e)
	}
}
