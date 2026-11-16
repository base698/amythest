package work

import (
	"database/sql"
	"strings"
	"unicode"
)

// Assignment is separate from collector provenance. It covers a session's old
// and future runs, and survives new inventory events and collector restarts.
const projectSchema = `BEGIN;
CREATE TABLE session_projects(session_id TEXT PRIMARY KEY REFERENCES sessions(id),project TEXT,version INTEGER NOT NULL);
CREATE VIEW effective_runs AS SELECT r.id,r.collector,r.session_id,r.binding_id,r.binding_version,r.card_id,COALESCE(p.project,r.project) AS project,r.started,r.finished,r.outcome,r.version,r.reviewed,r.historical FROM runs r LEFT JOIN session_projects p ON p.session_id=r.session_id;
CREATE VIEW effective_slots AS SELECT s.collector,s.pane,s.session_id,s.status,COALESCE(p.project,s.project) AS project,s.tab,s.last_output,s.inactivity FROM slots s LEFT JOIN session_projects p ON p.session_id=s.session_id;
PRAGMA user_version=2;
COMMIT;`

type SessionProject struct {
	ID            string `json:"session_id"`
	Collector     string `json:"collector"`
	Agent         string `json:"agent"`
	Provider      string `json:"provider_ref"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Project       string `json:"project"`
	SourceProject string `json:"source_project"`
	Assigned      bool   `json:"assigned"`
	Version       int64  `json:"project_version"`
}

func projectNames(tx *sql.Tx) ([]string, error) {
	out := []string{}
	rows, e := tx.Query(`SELECT project FROM (SELECT project FROM effective_runs UNION SELECT project FROM effective_slots UNION SELECT COALESCE(p.project,s.project) AS project FROM sessions s LEFT JOIN session_projects p ON p.session_id=s.id) WHERE project!='' ORDER BY project COLLATE NOCASE`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if e = rows.Scan(&p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func sessionProjects(tx *sql.Tx, project string) ([]SessionProject, error) {
	out := []SessionProject{}
	rows, e := tx.Query(`SELECT s.id,s.collector,s.agent,s.provider_ref,COALESCE(NULLIF(sl.tab,''),s.provider_ref),COALESCE(sl.status,'gone'),COALESCE(p.project,s.project),s.project,p.project IS NOT NULL,COALESCE(p.version,0) FROM sessions s LEFT JOIN session_projects p ON p.session_id=s.id LEFT JOIN slots sl ON sl.session_id=s.id WHERE (?='' OR COALESCE(p.project,s.project)=?) ORDER BY s.created DESC,s.id LIMIT 1000`, project, project)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var p SessionProject
		if e = rows.Scan(&p.ID, &p.Collector, &p.Agent, &p.Provider, &p.Title, &p.Status, &p.Project, &p.SourceProject, &p.Assigned, &p.Version); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) AssignProject(request, session, project string, expected int64) (int64, error) {
	project = strings.TrimSpace(project)
	if !validID(session) || expected < 0 || len(project) > 200 || strings.ContainsFunc(project, unicode.IsControl) {
		return 0, invalid("invalid session project")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var version int64
	h := hash([]any{"project", session, project, expected})
	if ok, e := receipt(tx, "owner", request, h, &version); e != nil || ok {
		return version, e
	}
	var exists int
	if e = tx.QueryRow(`SELECT 1 FROM sessions WHERE id=?`, session).Scan(&exists); e == sql.ErrNoRows {
		return 0, invalid("unknown session")
	} else if e != nil {
		return 0, e
	}
	if e = tx.QueryRow(`SELECT COALESCE((SELECT version FROM session_projects WHERE session_id=?),0)`, session).Scan(&version); e != nil {
		return 0, e
	}
	if version != expected {
		return 0, conflict("project assignment changed")
	}
	names, e := projectNames(tx)
	if e != nil {
		return 0, e
	}
	for _, name := range names {
		if strings.EqualFold(name, project) {
			project = name
			break
		}
	}
	version++
	if _, e = tx.Exec(`INSERT INTO session_projects VALUES(?,NULLIF(?,''),?) ON CONFLICT(session_id) DO UPDATE SET project=excluded.project,version=excluded.version`, session, project, version); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(`INSERT INTO audit(actor,action,subject,at) VALUES('owner','project',?,?)`, session+":"+project, nowText(s.now())); e != nil {
		return 0, e
	}
	if e = saveReceipt(tx, "owner", request, h, version); e != nil {
		return 0, e
	}
	return version, tx.Commit()
}
