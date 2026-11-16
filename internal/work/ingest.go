package work

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type Event struct {
	Seq            int64     `json:"seq"`
	At             time.Time `json:"at"`
	Kind           string    `json:"kind"`
	Session        string    `json:"session_id"`
	Run            string    `json:"run_id"`
	Pane           string    `json:"pane"`
	Binding        string    `json:"binding_id"`
	BindingVersion int64     `json:"binding_version"`
	Project        string    `json:"project"`
	Tab            string    `json:"tab"`
	Agent          string    `json:"agent"`
	ProviderRef    string    `json:"provider_session_ref"`
	Status         string    `json:"status"`
	Outcome        string    `json:"outcome"`
	Historical     bool      `json:"historical"`
	Artifact       *Artifact `json:"artifact,omitempty"`
	DocumentID     string    `json:"document_id"`
	Role           string    `json:"role"`
	Text           string    `json:"text"`
}
type Batch struct {
	Generation string  `json:"generation"`
	Events     []Event `json:"events"`
}
type Ack struct {
	Generation string `json:"generation"`
	Acked      int64  `json:"acked"`
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) Stream(collector, generation string) (Ack, error) {
	a := Ack{Generation: generation}
	if !validID(collector) || !validID(generation) {
		return a, invalid("collector and generation required")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return a, e
	}
	defer tx.Rollback()
	var current, state string
	e = tx.QueryRow(`SELECT generation FROM streams WHERE collector=? AND state='active'`, collector).Scan(&current)
	if e != nil && e != sql.ErrNoRows {
		return a, e
	}
	if current != "" && current != generation {
		return a, conflict("another generation is active; an owner must reset it")
	}
	e = tx.QueryRow(`SELECT acked,state FROM streams WHERE collector=? AND generation=?`, collector, generation).Scan(&a.Acked, &state)
	if e == sql.ErrNoRows {
		_, e = tx.Exec(`INSERT INTO streams(collector,generation,state) VALUES(?,?,'active')`, collector, generation)
	} else if e == nil && state != "active" {
		return a, conflict("generation is closed")
	}
	if e != nil {
		return a, e
	}
	return a, tx.Commit()
}
func (s *Store) Reset(collector string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`UPDATE streams SET state='closed' WHERE collector=?`, collector)
	if e == nil {
		_, e = tx.Exec(`UPDATE runs SET finished=?,outcome='unknown',version=version+1 WHERE collector=? AND finished=''`, nowText(s.now()), collector)
	}
	if e == nil {
		_, e = tx.Exec(`INSERT INTO audit(actor,action,subject,at) VALUES('owner','reset',?,?)`, collector, nowText(s.now()))
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Ingest(collector string, b Batch) (Ack, error) {
	a := Ack{Generation: b.Generation}
	if !validID(b.Generation) || len(b.Events) == 0 || len(b.Events) > 500 {
		return a, invalid("batch requires generation and 1–500 events")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return a, e
	}
	defer tx.Rollback()
	var state string
	e = tx.QueryRow(`SELECT acked,state FROM streams WHERE collector=? AND generation=?`, collector, b.Generation).Scan(&a.Acked, &state)
	if e == sql.ErrNoRows {
		return a, conflict("register stream first")
	}
	if e != nil {
		return a, e
	}
	if state != "active" {
		return a, conflict("generation closed")
	}
	original := a.Acked
	last := int64(0)
	for _, ev := range b.Events {
		if ev.Seq <= 0 || last > 0 && ev.Seq != last+1 {
			return a, &Fault{Status: 409, Message: "batch is not contiguous", Acked: original, Expected: a.Acked + 1, Seq: ev.Seq}
		}
		last = ev.Seq
		if ev.Seq <= a.Acked {
			var old string
			e = tx.QueryRow(`SELECT hash FROM events WHERE collector=? AND generation=? AND seq=? AND kind!='quarantined'`, collector, b.Generation, ev.Seq).Scan(&old)
			if e != nil && e != sql.ErrNoRows {
				return a, e
			}
			if e == nil && old != hash(ev) {
				return a, conflict("acknowledged event content changed")
			}
			continue
		}
		if ev.Seq != a.Acked+1 {
			return a, &Fault{Status: 409, Message: "sequence gap", Acked: original, Expected: a.Acked + 1, Seq: ev.Seq}
		}
		if ev.At.IsZero() || ev.At.After(s.now().Add(24*time.Hour)) {
			return a, wrapSeq(invalid("invalid occurrence time"), original, ev.Seq)
		}
		if e = s.apply(tx, collector, ev); e != nil {
			return Ack{Generation: b.Generation, Acked: original}, wrapSeq(e, original, ev.Seq)
		}
		payload, _ := json.Marshal(ev)
		_, e = tx.Exec(`INSERT INTO events VALUES(?,?,?,?,?,?,?,?)`, collector, b.Generation, ev.Seq, nowText(s.now()), nowText(ev.At), ev.Kind, string(payload), hash(ev))
		if e != nil {
			return a, e
		}
		a.Acked = ev.Seq
	}
	_, e = tx.Exec(`UPDATE streams SET acked=? WHERE collector=? AND generation=?`, a.Acked, collector, b.Generation)
	if e != nil {
		return a, e
	}
	if e = tx.Commit(); e != nil {
		return Ack{Generation: b.Generation, Acked: original}, e
	}
	return a, nil
}
func (s *Store) apply(tx *sql.Tx, collector string, ev Event) error {
	if !validID(ev.Session) {
		return invalid("session_id required")
	}
	if len(ev.Project) > 200 || len(ev.Tab) > 200 || len(ev.Agent) > 100 || len(ev.ProviderRef) > 200 {
		return invalid("session metadata too long")
	}
	if ev.Kind == "slot.seen" {
		if !validID(ev.Pane) {
			return invalid("pane required")
		}
		var owner, pane string
		e := tx.QueryRow(`SELECT collector,pane FROM sessions WHERE id=?`, ev.Session).Scan(&owner, &pane)
		if e == nil && (owner != collector || pane != ev.Pane) {
			return invalid("session belongs to another slot")
		}
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		_, e = tx.Exec(`INSERT INTO sessions VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, ev.Session, collector, ev.Pane, ev.ProviderRef, ev.Agent, ev.Project, nowText(ev.At))
		if e != nil {
			return e
		}
		if ev.Historical {
			return nil
		}
		_, e = tx.Exec(`INSERT INTO slots(collector,pane,session_id,status,project,tab,last_output) VALUES(?,?,?,'idle',?,?,?) ON CONFLICT(collector,pane) DO UPDATE SET session_id=excluded.session_id,status=CASE WHEN slots.session_id=excluded.session_id THEN slots.status ELSE 'idle' END,project=excluded.project,tab=excluded.tab`, collector, ev.Pane, ev.Session, ev.Project, ev.Tab, nowText(ev.At))
		return e
	}
	var owner, pane, project string
	e := tx.QueryRow(`SELECT collector,pane,project FROM sessions WHERE id=?`, ev.Session).Scan(&owner, &pane, &project)
	if e == sql.ErrNoRows || e == nil && owner != collector {
		return invalid("unknown or foreign session")
	}
	if e != nil {
		return e
	}
	switch ev.Kind {
	case "slot.status", "slot.gone":
		status := ev.Status
		if ev.Kind == "slot.gone" {
			status = "gone"
		}
		switch status {
		case "working", "blocked", "idle", "done", "gone":
		default:
			return invalid("unknown slot status")
		}
		if ev.Historical {
			return nil
		}
		_, e = tx.Exec(`UPDATE slots SET status=? WHERE collector=? AND pane=? AND session_id=?`, status, collector, pane, ev.Session)
		return e
	case "run.start":
		if !validID(ev.Run) {
			return invalid("run_id required")
		}
		card := ""
		if ev.Binding != "" {
			var bc, bs string
			var version int64
			e = tx.QueryRow(`SELECT collector,session_id,version,card_id FROM bindings WHERE id=?`, ev.Binding).Scan(&bc, &bs, &version, &card)
			if e != nil || bc != collector || bs != ev.Session || version != ev.BindingVersion {
				return invalid("binding snapshot does not match session")
			}
		} else if ev.BindingVersion != 0 {
			return invalid("binding_id required for version")
		}
		var existing string
		e = tx.QueryRow(`SELECT id FROM runs WHERE id=?`, ev.Run).Scan(&existing)
		if e == nil {
			return invalid("run ID already exists")
		}
		if e != sql.ErrNoRows {
			return e
		}
		if !ev.Historical {
			var n int
			if e = tx.QueryRow(`SELECT count(*) FROM runs WHERE session_id=? AND finished='' AND historical=0`, ev.Session).Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				return invalid("session already has an active run")
			}
		}
		_, e = tx.Exec(`INSERT INTO runs(id,collector,session_id,binding_id,binding_version,card_id,project,started,historical) VALUES(?,?,?,?,?,?,?,?,?)`, ev.Run, collector, ev.Session, ev.Binding, ev.BindingVersion, card, project, nowText(ev.At), ev.Historical)
		return e
	}
	if !validID(ev.Run) {
		return invalid("run_id required")
	}
	var rc, rs, rb, card string
	var version int64
	e = tx.QueryRow(`SELECT collector,session_id,binding_id,binding_version,card_id FROM runs WHERE id=?`, ev.Run).Scan(&rc, &rs, &rb, &version, &card)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if e == sql.ErrNoRows || rc != collector || rs != ev.Session {
		return invalid("unknown or foreign run")
	}
	if ev.Binding != rb || ev.BindingVersion != version {
		return invalid("event binding differs from immutable run snapshot")
	}
	switch ev.Kind {
	case "run.finish":
		switch ev.Outcome {
		case "success", "failure", "cancelled", "unknown", "abandoned":
		default:
			return invalid("unknown outcome")
		}
		var finished string
		if e = tx.QueryRow(`SELECT finished FROM runs WHERE id=?`, ev.Run).Scan(&finished); e != nil {
			return e
		}
		if finished != "" {
			return invalid("run already finished")
		}
		_, e = tx.Exec(`UPDATE runs SET finished=?,outcome=?,version=version+1 WHERE id=?`, nowText(ev.At), ev.Outcome, ev.Run)
		return e
	case "artifact":
		a := ev.Artifact
		if a == nil || a.Key == "" || len(a.Key) > 1024 || len(a.Title) > 1000 || len(a.Ref) > 4096 || len(a.Repo) > 2048 || len(a.RemotePath) > 4096 {
			return invalid("invalid artifact")
		}
		switch a.Kind {
		case "commit", "pr", "image", "video", "imageset", "file", "note", "message", "observation":
		default:
			return invalid("unknown artifact kind")
		}
		if a.MediaHash != "" && !validHash(a.MediaHash) {
			return invalid("invalid media hash")
		}
		var artifactID, ref, kind, mediaHash string
		e = tx.QueryRow(`SELECT id,ref,kind,media_hash FROM artifacts WHERE identity_key=?`, a.Key).Scan(&artifactID, &ref, &kind, &mediaHash)
		if e == sql.ErrNoRows {
			artifactID = id()
			_, e = tx.Exec(`INSERT INTO artifacts VALUES(?,?,?,?,?,?,?,?,?)`, artifactID, a.Key, a.Kind, a.Ref, a.Title, a.Repo, a.MediaHash, a.RemotePath, nowText(ev.At))
		} else if e == nil && (ref != a.Ref || kind != a.Kind || mediaHash != "" && a.MediaHash != "" && mediaHash != a.MediaHash) {
			return invalid("artifact identity has conflicting content")
		}
		if e != nil {
			return e
		}
		attribution := a.Attribution
		switch attribution {
		case "exact", "branch", "path", "suggested", "manual", "":
		default:
			return invalid("invalid attribution")
		}
		if attribution == "manual" {
			return invalid("collectors cannot claim manual attribution")
		}
		if attribution != "exact" {
			card = ""
		}
		var n int
		if e = tx.QueryRow(`SELECT count(*) FROM links WHERE artifact_id=? AND run_id=?`, artifactID, ev.Run).Scan(&n); e != nil {
			return e
		}
		if a.MediaHash != "" {
			_, e = tx.Exec(`UPDATE artifacts SET media_hash=? WHERE id=? AND media_hash=''`, a.MediaHash, artifactID)
			if e != nil {
				return e
			}
		}
		if n > 0 {
			if mediaHash == "" && a.MediaHash != "" {
				_, e = tx.Exec(`UPDATE runs SET version=version+1 WHERE id=?`, ev.Run)
				return e
			}
			return nil
		}
		_, e = tx.Exec(`INSERT INTO links(id,artifact_id,run_id,card_id,attribution,featured) VALUES(?,?,?,?,?,?)`, id(), artifactID, ev.Run, card, attribution, false)
		if e != nil {
			return e
		}
		return s.addDocument(tx, Document{Key: "artifact:" + artifactID, ArtifactID: artifactID, Kind: a.Kind, Title: a.Title, Body: a.Ref, Run: ev.Run, Created: nowText(ev.At)})
	case "chat", "observation":
		if !validID(ev.DocumentID) || len(ev.Text) > 65536 {
			return invalid("document ID required; text limit 64 KiB")
		}
		kind := ev.Kind
		if kind == "chat" {
			switch ev.Role {
			case "user", "assistant", "tool_call":
			default:
				return invalid("unsupported chat role")
			}
		}
		return s.addDocument(tx, Document{Key: collector + ":" + ev.DocumentID, Kind: kind, Title: ev.Role, Body: ev.Text, Run: ev.Run, Created: nowText(ev.At)})
	default:
		return invalid("unsupported event kind")
	}
}
func (s *Store) addDocument(tx *sql.Tx, d Document) error {
	var run, body string
	e := tx.QueryRow(`SELECT run_id,body FROM documents WHERE key=?`, d.Key).Scan(&run, &body)
	if e == nil {
		if d.ArtifactID != "" {
			_, e = tx.Exec(`UPDATE runs SET version=version+1 WHERE id=?`, d.Run)
			return e
		}
		if d.ArtifactID == "" && (run != d.Run || body != d.Body) {
			return invalid("document ID content changed")
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	_, e = tx.Exec(`UPDATE runs SET version=version+1 WHERE id=?`, d.Run)
	if e != nil {
		return e
	}
	if e = tx.QueryRow(`SELECT version FROM runs WHERE id=?`, d.Run).Scan(&d.Version); e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO documents VALUES(?,?,?,?,?,?,?,?)`, d.Key, d.Kind, d.Title, d.Body, d.Run, d.Created, d.Version, d.ArtifactID)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO work_fts(key,title,body) VALUES(?,?,?)`, d.Key, d.Title, d.Body)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`UPDATE slots SET last_output=MAX(last_output,?) WHERE session_id=(SELECT session_id FROM runs WHERE id=? AND historical=0)`, d.Created, d.Run)
	return e
}

// Heartbeat reports current connectivity, never replayed occurrence timestamps.
func (s *Store) Heartbeat(collector, name string, syncing bool, inactivity map[string]int64) error {
	if len(name) > 100 || len(inactivity) > 1000 {
		return invalid("heartbeat too large")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO machines VALUES(?,?,?,?) ON CONFLICT(collector) DO UPDATE SET name=excluded.name,last_contact=excluded.last_contact,syncing=excluded.syncing`, collector, name, nowText(s.now()), syncing)
	if e != nil {
		return e
	}
	for session, seconds := range inactivity {
		if seconds < 0 || seconds > 31536000 {
			return invalid("invalid inactivity interval")
		}
		_, e = tx.Exec(`UPDATE slots SET inactivity=? WHERE collector=? AND session_id=?`, seconds, collector, session)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Bindings(collector string) ([]Binding, error) {
	rows, e := s.db.Query(`SELECT id,collector,pane,session_id,version,card_id,acked FROM bindings WHERE collector=? AND active=1`, collector)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		if e = rows.Scan(&b.ID, &b.Collector, &b.Pane, &b.Session, &b.Version, &b.Card, &b.Acknowledged); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) AcknowledgeBinding(collector, binding string, version int64) error {
	r, e := s.db.Exec(`UPDATE bindings SET acked=1 WHERE collector=? AND id=? AND version=? AND active=1`, collector, binding, version)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return conflict("binding changed")
	}
	return nil
}
func (s *Store) Bind(actor, request, collector, session, card string, expected int64) (Binding, error) {
	var b Binding
	if card != "" && !validID(card) {
		return b, invalid("invalid card")
	}
	inputHash := hash([]any{"bind", collector, session, card, expected})
	tx, e := s.db.Begin()
	if e != nil {
		return b, e
	}
	defer tx.Rollback()
	if ok, e := receipt(tx, actor, request, inputHash, &b); e != nil || ok {
		return b, e
	}
	var pane string
	e = tx.QueryRow(`SELECT pane FROM sessions WHERE id=? AND collector=?`, session, collector).Scan(&pane)
	if e != nil {
		return b, invalid("unknown collector session")
	}
	var current string
	e = tx.QueryRow(`SELECT session_id FROM slots WHERE collector=? AND pane=?`, collector, pane).Scan(&current)
	if e != nil || current != session {
		return b, conflict("pane session changed")
	}
	var version int64
	if e = tx.QueryRow(`SELECT COALESCE(MAX(version),0) FROM bindings WHERE collector=? AND pane=?`, collector, pane).Scan(&version); e != nil {
		return b, e
	}
	if version != expected {
		return b, conflict("binding version changed")
	}
	_, e = tx.Exec(`UPDATE bindings SET active=0 WHERE collector=? AND pane=?`, collector, pane)
	if e != nil {
		return b, e
	}
	b = Binding{ID: id(), Collector: collector, Pane: pane, Session: session, Version: version + 1, Card: card}
	_, e = tx.Exec(`INSERT INTO bindings VALUES(?,?,?,?,?,?,1,0,?)`, b.ID, collector, pane, session, b.Version, card, nowText(s.now()))
	if e == nil {
		e = saveReceipt(tx, actor, request, inputHash, b)
	}
	if e != nil {
		return b, e
	}
	return b, tx.Commit()
}
func (s *Store) Quarantine(collector, generation string, seq int64, reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 1000 {
		return invalid("quarantine reason required")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var ack int64
	e = tx.QueryRow(`SELECT acked FROM streams WHERE collector=? AND generation=? AND state='active'`, collector, generation).Scan(&ack)
	if e != nil || seq != ack+1 {
		return conflict("only next pending sequence can be quarantined")
	}
	_, e = tx.Exec(`INSERT INTO events VALUES(?,?,?,?,?,'quarantined',?,?)`, collector, generation, seq, nowText(s.now()), nowText(s.now()), reason, hash(reason))
	if e != nil {
		return e
	}

	_, e = tx.Exec(`UPDATE streams SET acked=? WHERE collector=? AND generation=?`, seq, collector, generation)
	if e == nil {
		_, e = tx.Exec(`INSERT INTO audit(actor,action,subject,at) VALUES('owner',?,?,?)`, "quarantine: "+reason, collector+"/"+generation, nowText(s.now()))
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
