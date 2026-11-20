package work

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
)

// A milestone is a named stretch of work on a project, such as "production
// deploy": started when the work begins and ended when it is done. While it
// is open, everything the project produces belongs to it; afterwards its
// window collects the project's sessions, runs and artifacts (with what
// other stores keep, such as messages put on the project's wall) for review.
// Nothing is stamped as it arrives, so late events still land in the right
// milestone and collectors need no changes.

const milestoneSchema = `BEGIN;
CREATE TABLE milestones(id TEXT PRIMARY KEY,project TEXT NOT NULL,name TEXT NOT NULL,started TEXT NOT NULL,ended TEXT NOT NULL DEFAULT '');
CREATE INDEX milestones_project ON milestones(project,started);
PRAGMA user_version=3;
COMMIT;`

type Milestone struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Started string `json:"started_at"`
	Ended   string `json:"ended_at,omitempty"`
}

func (m Milestone) Open() bool { return m.Ended == "" }

func validName(s string, max int) bool {
	if s == "" || len(s) > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339Nano) }

// StartMilestone opens a milestone on a project. Starting one that is
// already open returns it unchanged.
func (s *Store) StartMilestone(project, name string) (Milestone, error) {
	if !validName(project, 200) || !validName(name, 200) {
		return Milestone{}, invalid("a milestone needs a project and a name")
	}
	if m, e := s.openMilestone(project, name); e == nil {
		return m, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return Milestone{}, e
	}
	m := Milestone{ID: id(), Project: project, Name: name, Started: s.stamp()}
	_, e := s.db.Exec(`INSERT INTO milestones(id,project,name,started) VALUES(?,?,?,?)`, m.ID, m.Project, m.Name, m.Started)
	return m, e
}

func (s *Store) openMilestone(project, name string) (Milestone, error) {
	var m Milestone
	e := s.db.QueryRow(`SELECT id,project,name,started,ended FROM milestones WHERE project=? AND name=? AND ended='' ORDER BY started DESC LIMIT 1`, project, name).
		Scan(&m.ID, &m.Project, &m.Name, &m.Started, &m.Ended)
	return m, e
}

// EndMilestone closes an open milestone; ending a closed one is harmless.
func (s *Store) EndMilestone(milestone string) (Milestone, error) {
	m, e := s.Milestone(milestone)
	if e != nil || !m.Open() {
		return m, e
	}
	m.Ended = s.stamp()
	_, e = s.db.Exec(`UPDATE milestones SET ended=? WHERE id=? AND ended=''`, m.Ended, m.ID)
	return m, e
}

// Milestone is one milestone by ID.
func (s *Store) Milestone(milestone string) (Milestone, error) {
	var m Milestone
	e := s.db.QueryRow(`SELECT id,project,name,started,ended FROM milestones WHERE id=?`, milestone).Scan(&m.ID, &m.Project, &m.Name, &m.Started, &m.Ended)
	if errors.Is(e, sql.ErrNoRows) {
		return m, &Fault{Status: 404, Message: "milestone not found"}
	}
	return m, e
}

// Milestones lists milestones, newest first: one project's or all, and
// only open ones when asked.
func (s *Store) Milestones(project string, open bool) ([]Milestone, error) {
	rows, e := s.db.Query(`SELECT id,project,name,started,ended FROM milestones WHERE (?='' OR project=?) AND (?=0 OR ended='') ORDER BY started DESC LIMIT 500`, project, project, open)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Milestone{}
	for rows.Next() {
		var m Milestone
		if e = rows.Scan(&m.ID, &m.Project, &m.Name, &m.Started, &m.Ended); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Collect is a milestone's project snapshot over its window (up to now while
// it is open): sessions, runs and their artifacts.
func (s *Store) Collect(milestone string) (Milestone, CardData, error) {
	m, e := s.Milestone(milestone)
	if e != nil {
		return m, CardData{}, e
	}
	before := m.Ended
	if before == "" {
		before = s.stamp()
	}
	d, e := s.SnapshotRange("", m.Project, m.Started, before)
	return m, d, e
}
