package work

import (
	"database/sql"
	"errors"
	"regexp"
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

// Links name things that belong to a milestone whatever their time: a
// message (its Messages ID) or a pull request (owner/repo#N, or its URL off
// github.com). Adding a link twice is harmless.
const milestoneLinkSchema = `BEGIN;
CREATE TABLE milestone_links(milestone_id TEXT NOT NULL REFERENCES milestones(id),kind TEXT NOT NULL CHECK(kind IN('message','pr')),ref TEXT NOT NULL,added_at TEXT NOT NULL,added_by TEXT NOT NULL,PRIMARY KEY(milestone_id,kind,ref));
PRAGMA user_version=4;
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

func (s *Store) stamp() string { return nowText(s.now()) }

// MilestoneTime reads a milestone time given as RFC 3339 and returns it in
// the store's sortable UTC form ("" stays "").
func MilestoneTime(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	t, e := time.Parse(time.RFC3339Nano, v)
	if e != nil {
		return "", invalid("milestone times are RFC 3339, such as 2026-10-01T00:00:00Z")
	}
	return nowText(t), nil
}

// StartMilestone opens a milestone on a project, from now or from a past
// time. Starting one with the same name that is already open returns it
// unchanged, unless an end is given: that records a finished stretch of
// past work, which may overlap others on the project.
func (s *Store) StartMilestone(project, name, started, ended string) (Milestone, error) {
	if !validName(project, 200) || !validName(name, 200) {
		return Milestone{}, invalid("a milestone needs a project and a name")
	}
	var e error
	if started, e = MilestoneTime(started); e != nil {
		return Milestone{}, e
	}
	if ended, e = MilestoneTime(ended); e != nil {
		return Milestone{}, e
	}
	if ended == "" {
		if m, e := s.openMilestone(project, name); e == nil {
			return m, nil
		} else if !errors.Is(e, sql.ErrNoRows) {
			return Milestone{}, e
		}
	}
	if started == "" {
		started = s.stamp()
	}
	if ended != "" && ended < started {
		return Milestone{}, invalid("a milestone cannot end before it starts")
	}
	m := Milestone{ID: id(), Project: project, Name: name, Started: started, Ended: ended}
	_, e = s.db.Exec(`INSERT INTO milestones(id,project,name,started,ended) VALUES(?,?,?,?,?)`, m.ID, m.Project, m.Name, m.Started, m.Ended)
	return m, e
}

// MilestoneChange edits a milestone; nil fields stay as they are. An empty
// Ended reopens it.
type MilestoneChange struct {
	Name    *string `json:"name,omitempty"`
	Started *string `json:"started_at,omitempty"`
	Ended   *string `json:"ended_at,omitempty"`
}

// UpdateMilestone renames a milestone or moves its start or end.
func (s *Store) UpdateMilestone(milestone string, c MilestoneChange) (Milestone, error) {
	m, e := s.Milestone(milestone)
	if e != nil {
		return m, e
	}
	if c.Name != nil {
		if !validName(*c.Name, 200) {
			return Milestone{}, invalid("a milestone needs a name")
		}
		m.Name = *c.Name
	}
	if c.Started != nil {
		if *c.Started == "" {
			return Milestone{}, invalid("a milestone needs a start")
		}
		if m.Started, e = MilestoneTime(*c.Started); e != nil {
			return Milestone{}, e
		}
	}
	if c.Ended != nil {
		if m.Ended, e = MilestoneTime(*c.Ended); e != nil {
			return Milestone{}, e
		}
	}
	if m.Ended != "" && m.Ended < m.Started {
		return Milestone{}, invalid("a milestone cannot end before it starts")
	}
	_, e = s.db.Exec(`UPDATE milestones SET name=?,started=?,ended=? WHERE id=?`, m.Name, m.Started, m.Ended, m.ID)
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
// it is open): sessions, runs and their artifacts, and its links.
func (s *Store) Collect(milestone string) (Milestone, CardData, []MilestoneLink, error) {
	m, e := s.Milestone(milestone)
	if e != nil {
		return m, CardData{}, nil, e
	}
	before := m.Ended
	if before == "" {
		before = s.stamp()
	}
	d, e := s.SnapshotRange("", m.Project, m.Started, before)
	if e != nil {
		return m, d, nil, e
	}
	links, e := s.MilestoneLinks(m.ID)
	return m, d, links, e
}

// MilestoneLink is one thing linked to a milestone. A pull request the
// collector saw in an agent session carries that artifact.
type MilestoneLink struct {
	Milestone string    `json:"milestone_id"`
	Kind      string    `json:"kind"`
	Ref       string    `json:"ref"`
	Added     string    `json:"added_at"`
	AddedBy   string    `json:"added_by"`
	Artifact  *Artifact `json:"artifact,omitempty"`
}

var (
	prShort = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([1-9][0-9]{0,9})$`)
	prURL   = regexp.MustCompile(`^https://([A-Za-z0-9.-]+(?::[0-9]+)?)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/([1-9][0-9]{0,9})/?(?:[?#].*)?$`)
)

// LinkRef checks a link and returns its stored form: a pull request on
// github.com becomes owner/repo#N.
func LinkRef(kind, ref string) (string, error) {
	switch kind {
	case "message":
		if !validName(ref, 200) || strings.ContainsAny(ref, " \t") {
			return "", invalid("a message link needs its Messages ID")
		}
		return ref, nil
	case "pr":
		if prShort.MatchString(ref) {
			return ref, nil
		}
		if m := prURL.FindStringSubmatch(ref); m != nil {
			if strings.EqualFold(m[1], "github.com") {
				return m[2] + "/" + m[3] + "#" + m[4], nil
			}
			return "https://" + m[1] + "/" + m[2] + "/" + m[3] + "/pull/" + m[4], nil
		}
		return "", invalid("a pull request link is owner/repo#N or the pull request's URL")
	}
	return "", invalid("a milestone link is a message or a pr")
}

// prURLOf is the web address of a stored pull request ref.
func prURLOf(ref string) string {
	if m := prShort.FindStringSubmatch(ref); m != nil {
		return "https://github.com/" + m[1] + "/" + m[2] + "/pull/" + m[3]
	}
	return ref
}

// LinkMilestone links a message or pull request to a milestone.
func (s *Store) LinkMilestone(milestone, kind, ref, by string) (MilestoneLink, error) {
	if _, e := s.Milestone(milestone); e != nil {
		return MilestoneLink{}, e
	}
	ref, e := LinkRef(kind, ref)
	if e != nil {
		return MilestoneLink{}, e
	}
	if _, e = s.db.Exec(`INSERT INTO milestone_links(milestone_id,kind,ref,added_at,added_by) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, milestone, kind, ref, s.stamp(), by); e != nil {
		return MilestoneLink{}, e
	}
	l := MilestoneLink{Milestone: milestone, Kind: kind, Ref: ref}
	e = s.db.QueryRow(`SELECT added_at,added_by FROM milestone_links WHERE milestone_id=? AND kind=? AND ref=?`, milestone, kind, ref).Scan(&l.Added, &l.AddedBy)
	return l, e
}

// UnlinkMilestone removes a link; removing a missing one is harmless.
func (s *Store) UnlinkMilestone(milestone, kind, ref string) error {
	ref, e := LinkRef(kind, ref)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(`DELETE FROM milestone_links WHERE milestone_id=? AND kind=? AND ref=?`, milestone, kind, ref)
	return e
}

// MilestoneLinks lists a milestone's links in the order they were added,
// each pull request with the artifact the collector recorded for it.
func (s *Store) MilestoneLinks(milestone string) ([]MilestoneLink, error) {
	rows, e := s.db.Query(`SELECT milestone_id,kind,ref,added_at,added_by FROM milestone_links WHERE milestone_id=? ORDER BY added_at,kind,ref`, milestone)
	if e != nil {
		return nil, e
	}
	out := []MilestoneLink{}
	for rows.Next() {
		var l MilestoneLink
		if e = rows.Scan(&l.Milestone, &l.Kind, &l.Ref, &l.Added, &l.AddedBy); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, l)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for i := range out {
		if out[i].Kind != "pr" {
			continue
		}
		var a Artifact
		e = s.db.QueryRow(`SELECT id,identity_key,kind,ref,title,repo,created FROM artifacts WHERE kind='pr' AND ref IN(?,?) ORDER BY created LIMIT 1`, prURLOf(out[i].Ref), prURLOf(out[i].Ref)+"/").
			Scan(&a.ID, &a.Key, &a.Kind, &a.Ref, &a.Title, &a.Repo, &a.Created)
		if e == nil {
			out[i].Artifact = &a
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	return out, nil
}
