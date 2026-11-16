package work

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Snapshot is bounded and uses one read transaction so review watermarks describe
// precisely the output displayed, even while a collector is delivering a batch.
func (s *Store) Snapshot(card, project string) (CardData, error) {
	return s.snapshot(card, project, "", true)
}
func (s *Store) Overview() (CardData, error) { return s.snapshot("", "", "", false) }
func (s *Store) RunSnapshot(run string) (CardData, error) {
	if !validID(run) {
		return CardData{}, invalid("run_id required")
	}
	return s.snapshot("", "", run, true)
}
func (s *Store) snapshot(card, project, run string, details bool) (CardData, error) {
	out := CardData{Runs: []Run{}, Artifacts: []Artifact{}, Documents: []Document{}, Slots: []Slot{}, Machines: []Machine{}, Projects: []string{}, Sessions: []SessionProject{}}
	tx, e := s.db.Begin()
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT `+runColumns+` FROM effective_runs WHERE (?='' OR card_id=?) AND (?='' OR project=?) AND (?='' OR id=?) ORDER BY started DESC,id LIMIT 200`, card, card, project, project, run, run)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Runs = append(out.Runs, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if details {
		rows, e = tx.Query(`SELECT a.id,a.identity_key,a.kind,a.ref,a.title,a.repo,a.media_hash,a.remote_path,l.id,l.run_id,l.card_id,l.attribution,l.featured,COALESCE(m.available,0),a.created FROM links l JOIN artifacts a ON a.id=l.artifact_id JOIN effective_runs r ON r.id=l.run_id LEFT JOIN media_owners mo ON mo.hash=a.media_hash AND mo.collector=r.collector LEFT JOIN media m ON m.hash=mo.hash WHERE (?='' OR l.card_id=? OR r.card_id=?) AND (?='' OR r.project=?) AND (?='' OR r.id=?) ORDER BY a.created DESC,a.id,l.id LIMIT 500`, card, card, card, project, project, run, run)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var a Artifact
			if e = rows.Scan(&a.ID, &a.Key, &a.Kind, &a.Ref, &a.Title, &a.Repo, &a.MediaHash, &a.RemotePath, &a.LinkID, &a.RunID, &a.Card, &a.Attribution, &a.Featured, &a.Available, &a.Created); e != nil {
				rows.Close()
				return out, e
			}
			out.Artifacts = append(out.Artifacts, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		rows, e = tx.Query(`SELECT d.key,d.kind,d.title,d.body,d.run_id,d.created,d.version FROM documents d JOIN effective_runs r ON r.id=d.run_id WHERE (?='' OR r.card_id=?) AND (?='' OR r.project=?) AND d.kind IN('chat','observation') AND (?='' OR r.id=?) ORDER BY d.created DESC,d.key LIMIT 200`, card, card, project, project, run, run)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var d Document
			if e = rows.Scan(&d.Key, &d.Kind, &d.Title, &d.Body, &d.Run, &d.Created, &d.Version); e != nil {
				rows.Close()
				return out, e
			}
			out.Documents = append(out.Documents, d)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	rows, e = tx.Query(`SELECT s.collector,s.pane,s.session_id,s.status,s.project,s.tab,s.last_output,s.inactivity,COALESCE(b.id,''),COALESCE(b.version,0),COALESCE(b.card_id,''),COALESCE(b.acked,0),(SELECT COALESCE(MAX(version),0) FROM bindings bv WHERE bv.collector=s.collector AND bv.pane=s.pane) FROM effective_slots s LEFT JOIN bindings b ON b.collector=s.collector AND b.pane=s.pane AND b.session_id=s.session_id AND b.active=1 WHERE (?='' OR b.card_id=?) AND (?='' OR s.project=?) ORDER BY s.collector,s.pane LIMIT 1000`, card, card, project, project)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var p Slot
		b := Binding{}
		if e = rows.Scan(&p.Collector, &p.Pane, &p.Session, &p.Status, &p.Project, &p.Tab, &p.LastOutput, &p.Inactivity, &b.ID, &b.Version, &b.Card, &b.Acknowledged, &p.BindingVersion); e != nil {
			rows.Close()
			return out, e
		}
		if b.ID != "" {
			b.Collector = p.Collector
			b.Pane = p.Pane
			b.Session = p.Session
			p.Binding = &b
		}
		out.Slots = append(out.Slots, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.Query(`SELECT collector,name,last_contact,syncing FROM machines ORDER BY name,collector LIMIT 1000`)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var m Machine
		if e = rows.Scan(&m.ID, &m.Name, &m.LastContact, &m.Syncing); e != nil {
			rows.Close()
			return out, e
		}
		t, _ := time.Parse(time.RFC3339Nano, m.LastContact)
		m.Online = s.now().Sub(t) <= s.cfg.AsleepAfter
		out.Machines = append(out.Machines, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if details {
		visible := map[string]int{}
		for _, a := range out.Artifacts {
			visible[a.RunID]++
		}
		for _, d := range out.Documents {
			visible[d.Run]++
		}
		for i := range out.Runs {
			var total int
			e = tx.QueryRow(`SELECT (SELECT count(*) FROM documents WHERE run_id=? AND kind IN('chat','observation'))+(SELECT count(*) FROM links WHERE run_id=?)`, out.Runs[i].ID, out.Runs[i].ID).Scan(&total)
			if e != nil {
				return out, e
			}
			out.Runs[i].Reviewable = visible[out.Runs[i].ID] == total
		}
	}

	if out.Projects, e = projectNames(tx); e != nil {
		return out, e
	}
	if out.Sessions, e = sessionProjects(tx, project); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) Review(run string, version int64) error {
	if version < 0 {
		return invalid("invalid displayed version")
	}
	result, e := s.db.Exec(`UPDATE runs SET reviewed=MAX(reviewed,?) WHERE id=? AND version>=?`, version, run, version)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return conflict("run missing or displayed version is ahead of output")
	}
	return nil
}

type Attention struct {
	Session     string `json:"session_id"`
	Project     string `json:"project"`
	Agent       string `json:"agent"`
	Finished    string `json:"finished_at"`
	Outcome     string `json:"outcome"`
	Preview     string `json:"preview"`
	OutputCount int    `json:"output_count"`
	Subject     string `json:"subject"`
	Reason      string `json:"reason"`
	Version     string `json:"version"`
	Card        string `json:"card_id"`
	Run         string `json:"run_id"`
	Collector   string `json:"collector"`
	Pane        string `json:"pane"`
	Title       string `json:"title"`
}

func (s *Store) Inbox() ([]Attention, error) {
	d, e := s.Overview()
	if e != nil {
		return nil, e
	}
	out := []Attention{}
	machines := map[string]Machine{}
	for _, m := range d.Machines {
		machines[m.ID] = m
		if !m.Online {
			out = append(out, Attention{Subject: "machine:" + m.ID, Reason: "offline", Version: m.LastContact, Collector: m.ID, Title: m.Name})
		}
	}

	pending, e := s.db.Query(`SELECT r.id,r.collector,r.session_id,r.card_id,r.project,r.finished,r.outcome,r.version,
 COALESCE(NULLIF(sl.tab,''),NULLIF(se.provider_ref,''),se.agent || ' session'),se.agent,
 COALESCE((SELECT substr(body,1,480) FROM documents WHERE run_id=r.id AND (kind='observation' OR (kind='chat' AND title='assistant')) ORDER BY created DESC,key DESC LIMIT 1),
 (SELECT a.title FROM links l JOIN artifacts a ON a.id=l.artifact_id WHERE l.run_id=r.id ORDER BY a.created DESC,a.id LIMIT 1),''),
 (SELECT count(*) FROM documents WHERE run_id=r.id AND kind IN('chat','observation'))+(SELECT count(*) FROM links WHERE run_id=r.id)
 FROM effective_runs r JOIN sessions se ON se.id=r.session_id LEFT JOIN slots sl ON sl.session_id=r.session_id
 WHERE r.historical=0 AND r.version>r.reviewed AND r.finished!=''
 AND NOT EXISTS(SELECT 1 FROM snoozes z WHERE z.subject='run:'||r.id AND z.version=CAST(r.version AS TEXT) AND z.until_at>?)
 ORDER BY r.finished DESC,r.id LIMIT 1000`, nowText(s.now()))
	if e != nil {
		return nil, e
	}
	for pending.Next() {
		var a Attention
		var version int64
		if e = pending.Scan(&a.Run, &a.Collector, &a.Session, &a.Card, &a.Project, &a.Finished, &a.Outcome, &version, &a.Title, &a.Agent, &a.Preview, &a.OutputCount); e != nil {
			pending.Close()
			return nil, e
		}
		a.Subject = "run:" + a.Run
		a.Reason = "needs-review"
		a.Version = fmt.Sprint(version)
		out = append(out, a)
	}
	e = pending.Err()
	pending.Close()
	if e != nil {
		return nil, e
	}

	for _, p := range d.Slots {
		m := machines[p.Collector]
		if !m.Online || m.Syncing {
			continue
		}
		reason := ""
		if p.Status == "blocked" {
			reason = "blocked"
		} else if p.Status == "working" && time.Duration(p.Inactivity)*time.Second >= s.cfg.StallAfter {
			reason = "stalled"
		}
		if reason != "" {
			card := ""
			if p.Binding != nil {
				card = p.Binding.Card
			}
			out = append(out, Attention{Subject: "slot:" + p.Collector + ":" + p.Pane, Reason: reason, Version: p.Session + ":" + p.LastOutput + ":" + p.Status, Card: card, Collector: p.Collector, Pane: p.Pane, Title: p.Tab, Session: p.Session, Project: p.Project})
		}
	}
	rows, e := s.db.Query(`SELECT subject,version FROM snoozes WHERE until_at>?`, nowText(s.now()))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	muted := map[string]string{}
	for rows.Next() {
		var subject, version string
		if e = rows.Scan(&subject, &version); e != nil {
			return nil, e
		}
		muted[subject] = version
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	filtered := out[:0]
	for _, a := range out {
		if muted[a.Subject] != a.Version {
			filtered = append(filtered, a)
		}
	}
	return filtered, nil
}
func (s *Store) Snooze(subject, version string, until time.Time) error {
	if subject == "" || version == "" || len(subject) > 400 || len(version) > 400 || !until.After(s.now()) || until.After(s.now().Add(7*24*time.Hour)) {
		return invalid("snooze requires a version and time within seven days")
	}
	_, e := s.db.Exec(`INSERT INTO snoozes VALUES(?,?,?) ON CONFLICT(subject) DO UPDATE SET version=excluded.version,until_at=excluded.until_at`, subject, version, nowText(until))
	return e
}

// Dismiss hides only the versions the owner saw. It does not mark output as
// reviewed or delete it; later output and changed states need attention again.
type AttentionVersion struct {
	Subject string `json:"subject"`
	Version string `json:"version"`
}

func (s *Store) Dismiss(items []AttentionVersion) error {
	if len(items) == 0 || len(items) > 1000 {
		return invalid("select between 1 and 1000 notifications")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, item := range items {
		if len(item.Subject) > 400 || item.Version == "" || len(item.Version) > 400 {
			return invalid("invalid notification")
		}
		var current string
		switch {
		case strings.HasPrefix(item.Subject, "run:"):
			e = tx.QueryRow(`SELECT CAST(version AS TEXT) FROM runs WHERE id=? AND finished!=''`, strings.TrimPrefix(item.Subject, "run:")).Scan(&current)
		case strings.HasPrefix(item.Subject, "machine:"):
			e = tx.QueryRow(`SELECT last_contact FROM machines WHERE collector=?`, strings.TrimPrefix(item.Subject, "machine:")).Scan(&current)
		case strings.HasPrefix(item.Subject, "slot:"):
			e = tx.QueryRow(`SELECT session_id||':'||last_output||':'||status FROM slots WHERE collector||':'||pane=?`, strings.TrimPrefix(item.Subject, "slot:")).Scan(&current)
		default:
			return invalid("unknown notification")
		}
		if e == sql.ErrNoRows || e == nil && current != item.Version {
			return conflict("notification changed; refresh before dismissing")
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO snoozes VALUES(?,?,'9999-12-31T23:59:59.000000000Z') ON CONFLICT(subject) DO UPDATE SET version=excluded.version,until_at=excluded.until_at`, item.Subject, item.Version); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO audit(actor,action,subject,at) VALUES('owner','dismiss',?,?)`, item.Subject, nowText(s.now())); e != nil {
			return e
		}
	}
	return tx.Commit()
}

type SearchOptions struct {
	Query   string `json:"query"`
	Card    string `json:"card_id,omitempty"`
	Project string `json:"project,omitempty"`
	Kind    string `json:"kind,omitempty"`
	After   string `json:"after,omitempty"`
	Before  string `json:"before,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
type SearchHit struct {
	Document
	Card    string `json:"card_id"`
	Project string `json:"project"`
}

func (s *Store) Search(q SearchOptions) ([]SearchHit, error) {
	out := []SearchHit{}
	if len(q.Query) > 1000 {
		return nil, invalid("query too long")
	}
	words := strings.Fields(q.Query)
	if len(words) == 0 {
		return out, nil
	}
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	match := strings.Join(words, " AND ")
	if q.Limit <= 0 {
		q.Limit = 30
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	for _, date := range []string{q.After, q.Before} {
		if date != "" {
			if _, e := time.Parse("2006-01-02", date); e != nil {
				return nil, invalid("dates must be YYYY-MM-DD")
			}
		}
	}
	rows, e := s.db.Query(`SELECT d.key,d.kind,d.title,snippet(work_fts,2,'','',' … ',32),d.run_id,d.created,d.version,
 CASE WHEN d.artifact_id='' THEN r.card_id ELSE COALESCE((SELECT MIN(l.card_id) FROM links l WHERE l.artifact_id=d.artifact_id AND (?='' OR l.card_id=?)),'') END,r.project
 FROM work_fts JOIN documents d ON d.key=work_fts.key JOIN effective_runs r ON r.id=d.run_id
 WHERE work_fts MATCH ? AND ((d.artifact_id='' AND (?='' OR r.card_id=?) AND (?='' OR r.project=?)) OR (d.artifact_id!='' AND EXISTS(SELECT 1 FROM links l JOIN effective_runs lr ON lr.id=l.run_id WHERE l.artifact_id=d.artifact_id AND (?='' OR l.card_id=?) AND (?='' OR lr.project=?))))
 AND (?='' OR d.kind=?) AND (?='' OR substr(d.created,1,10)>=?) AND (?='' OR substr(d.created,1,10)<=?) ORDER BY bm25(work_fts),d.key LIMIT ?`, q.Card, q.Card, match, q.Card, q.Card, q.Project, q.Project, q.Card, q.Card, q.Project, q.Project, q.Kind, q.Kind, q.After, q.After, q.Before, q.Before, q.Limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var h SearchHit
		if e = rows.Scan(&h.Key, &h.Kind, &h.Title, &h.Body, &h.Run, &h.Created, &h.Version, &h.Card, &h.Project); e != nil {
			return nil, e
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Prune raw text and its search projection atomically. Watermarks and identities
// remain, making retransmission of acknowledged, expired events inert.
func (s *Store) Prune(before time.Time) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	cut := nowText(before)
	for _, q := range []string{`DELETE FROM work_fts WHERE key IN(SELECT key FROM documents WHERE kind IN('chat','observation') AND created<?)`, `DELETE FROM documents WHERE kind IN('chat','observation') AND created<?`, `DELETE FROM events WHERE received<?`} {
		if _, e = tx.Exec(q, cut); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// File changes an association, never the run's recorded provenance.
func (s *Store) File(request, link, card string, featured bool) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var done bool
	h := hash([]any{"file", link, card, featured})
	if ok, e := receipt(tx, "owner", request, h, &done); e != nil || ok {
		return e
	}
	r, e := tx.Exec(`UPDATE links SET card_id=?,attribution='manual',filed_by='owner',featured=? WHERE id=?`, card, featured, link)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return invalid("unknown artifact link")
	}
	_, e = tx.Exec(`INSERT INTO audit(actor,action,subject,at) VALUES('owner','file',?,?)`, link+":"+card, nowText(s.now()))
	if e == nil {
		e = saveReceipt(tx, "owner", request, h, true)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
