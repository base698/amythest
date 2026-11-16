// Package hub provides a durable, single-writer delivery queue for Amythest Work.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Event struct {
	Seq            int64     `json:"seq"`
	At             time.Time `json:"at"`
	Kind           string    `json:"kind"`
	Session        string    `json:"session_id"`
	Run            string    `json:"run_id,omitempty"`
	Pane           string    `json:"pane,omitempty"`
	Binding        string    `json:"binding_id,omitempty"`
	BindingVersion int64     `json:"binding_version,omitempty"`
	Project        string    `json:"project,omitempty"`
	Tab            string    `json:"tab,omitempty"`
	Agent          string    `json:"agent,omitempty"`
	Provider       string    `json:"provider_session_ref,omitempty"`
	Status         string    `json:"status,omitempty"`
	Outcome        string    `json:"outcome,omitempty"`
	Historical     bool      `json:"historical,omitempty"`
	Artifact       *Artifact `json:"artifact,omitempty"`
	DocumentID     string    `json:"document_id,omitempty"`
	Role           string    `json:"role,omitempty"`
	Text           string    `json:"text,omitempty"`
}
type Artifact struct {
	Key         string `json:"identity_key"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Repo        string `json:"repo"`
	MediaHash   string `json:"media_hash,omitempty"`
	RemotePath  string `json:"remote_path,omitempty"`
	Attribution string `json:"attribution"`
}
type Binding struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Session string `json:"session_id"`
	Card    string `json:"card_id"`
}
type Pane struct{ ID, Provider, Agent, Project, Tab, Dir, Status string }
type Session struct {
	Hooked     bool
	Turn       string
	Pane       Pane
	ID, Run    string
	Binding    Binding
	RunBinding Binding
	Started    time.Time
	Seen       map[string]bool
}
type Job struct {
	Session Session
	Outcome string
	At      time.Time
}
type State struct {
	MediaPending map[string]bool `json:",omitempty"`
	CodexOffsets map[string]int64
	HookEvents   map[string]bool
	HookFinished map[string]bool
	InventoryAt  time.Time
	Generation   string
	Next, Acked  int64
	Events       []Event
	Sessions     map[string]Session
	Jobs         []Job
	Client       string
	Server       string
}
type Queue struct {
	mediaMu sync.Mutex
	mu      sync.Mutex
	path    string
	lock    *os.File
	state   State
}

func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Open(dir string) (*Queue, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(filepath.Join(dir, "hub.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("another amythest-collector hub process is running")
	}
	q := &Queue{path: filepath.Join(dir, "hub.json"), lock: lock}
	b, e := os.ReadFile(q.path)
	if os.IsNotExist(e) {
		q.state = State{Generation: ID(), Next: 1, Sessions: map[string]Session{}}
		e = q.save(q.state)
	} else if e == nil {
		e = json.Unmarshal(b, &q.state)
		if e == nil && (q.state.Generation == "" || q.state.Next <= q.state.Acked || q.state.Sessions == nil) {
			e = errors.New("invalid queue state; explicit recovery required")
		}
	}
	if e != nil {
		q.Close()
		return nil, e
	}
	return q, nil
}
func (q *Queue) Close() { syscall.Flock(int(q.lock.Fd()), syscall.LOCK_UN); q.lock.Close() }
func (q *Queue) save(s State) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(q.path), ".hub-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), q.path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(q.path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (q *Queue) Update(fn func(*State) error) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	b, _ := json.Marshal(q.state)
	var next State
	if e := json.Unmarshal(b, &next); e != nil {
		return e
	}
	if e := fn(&next); e != nil {
		return e
	}
	if len(next.Events) > 100000 {
		return errors.New("hub queue full; delivery must recover before capture continues")
	}
	if e := q.save(next); e != nil {
		return e
	}
	q.state = next
	return nil
}
func (q *Queue) Snapshot() State {
	q.mu.Lock()
	defer q.mu.Unlock()
	b, _ := json.Marshal(q.state)
	var s State
	json.Unmarshal(b, &s)
	return s
}
func (s *State) append(ev Event) { ev.Seq = s.Next; s.Next++; s.Events = append(s.Events, ev) }
func (s *State) event(p Session, kind string, at time.Time) Event {
	return Event{At: at, Kind: kind, Session: p.ID, Run: p.Run, Pane: p.Pane.ID, Binding: p.RunBinding.ID, BindingVersion: p.RunBinding.Version}
}
func (q *Queue) Ack(generation string, acked int64) error {
	return q.Update(func(s *State) error {
		if generation != s.Generation || acked < s.Acked || acked >= s.Next {
			return fmt.Errorf("server acknowledgement outside local queue (%d)", acked)
		}
		s.Acked = acked
		i := 0
		for i < len(s.Events) && s.Events[i].Seq <= acked {
			i++
		}
		s.Events = s.Events[i:]
		return nil
	})
}
func (q *Queue) Bind(bindings []Binding) error {
	return q.Update(func(s *State) error {
		for pane, p := range s.Sessions {
			for _, b := range bindings {
				if b.Session == p.ID && b.Version >= p.Binding.Version {
					p.Binding = b
					s.Sessions[pane] = p
				}
			}
		}
		return nil
	})
}

// Inventory must be a fresh, complete local topology. Network disconnects do not
// call this with an empty inventory, and never finish a run by themselves.
func (q *Queue) Inventory(panes []Pane, at time.Time) error {
	return q.Update(func(s *State) error {
		s.InventoryAt = at
		present := map[string]bool{}
		for _, pane := range panes {
			if pane.ID == "" || pane.Agent == "" {
				continue
			}
			present[pane.ID] = true
			if pane.Agent == "hermes" && pane.Provider == "" {
				continue
			}
			p, ok := s.Sessions[pane.ID]
			// A direct lifecycle event may arrive before its pane snapshot. Keep
			// the hook's incarnation instead of synthesizing a duplicate run.
			hookedElsewhere := false
			for key, other := range s.Sessions {
				if key != pane.ID && other.Hooked && other.Pane.Provider == pane.Provider && pane.Provider != "" && other.Pane.Agent == pane.Agent {
					hookedElsewhere = true
					break
				}
			}
			if hookedElsewhere {
				continue
			}
			if ok && p.Hooked && (p.Pane.Provider != pane.Provider || p.Pane.Agent != pane.Agent) {
				s.Sessions[p.Pane.Agent+":"+hookKey(p.Pane.Provider, "")] = p
				delete(s.Sessions, pane.ID)
				ok = false
			}
			if ok && (p.Pane.Provider != pane.Provider || p.Pane.Agent != pane.Agent) {
				if p.Run != "" {
					ev := s.event(p, "run.finish", at)
					ev.Outcome = "unknown"
					s.append(ev)
				}
				gone := s.event(p, "slot.gone", at)
				s.append(gone)
				ok = false
			}
			if !ok {
				p = Session{Pane: pane, ID: ID(), Seen: map[string]bool{}}
				s.append(Event{At: at, Kind: "slot.seen", Session: p.ID, Pane: pane.ID, Provider: pane.Provider, Agent: pane.Agent, Project: pane.Project, Tab: pane.Tab})
			}
			if p.Hooked {
				s.Sessions[pane.ID] = p
				continue
			}
			previous := p.Pane.Status
			p.Pane = pane
			if pane.Status == "working" && p.Run == "" {
				p.Run = ID()
				p.Started = at
				p.RunBinding = p.Binding
				p.Seen = map[string]bool{}
				s.append(s.event(p, "run.start", at))
			}
			if !ok || previous != pane.Status {
				ev := s.event(p, "slot.status", at)
				ev.Status = pane.Status
				s.append(ev)
			}
			if p.Run != "" && (pane.Status == "done" || pane.Status == "idle") {
				outcome := "unknown"
				if pane.Status == "done" {
					outcome = "success"
				}
				ev := s.event(p, "run.finish", at)
				ev.Outcome = outcome
				s.append(ev)
				s.Jobs = append(s.Jobs, Job{Session: p, Outcome: outcome, At: at})
				p.Run = ""
			}
			s.Sessions[pane.ID] = p
		}
		for pane, p := range s.Sessions {
			if present[pane] || p.Hooked {
				continue
			}
			if p.Run != "" {
				ev := s.event(p, "run.finish", at)
				ev.Outcome = "abandoned"
				s.append(ev)
			}
			s.append(s.event(p, "slot.gone", at))
			delete(s.Sessions, pane)
		}
		return nil
	})
}
func (q *Queue) Complete(job Job, artifacts []Artifact) error {
	q.mediaMu.Lock()
	defer q.mediaMu.Unlock()
	exists := false
	for _, pending := range q.Snapshot().Jobs {
		if pending.Session.Run == job.Session.Run {
			exists = true
			break
		}
	}
	if !exists {
		return nil
	}
	uploads := map[string]bool{}
	for i, a := range artifacts {
		if a.MediaHash != "" {
			continue
		}
		digest, e := q.stageMedia(a)
		if e != nil {
			fmt.Fprintln(os.Stderr, "artifact remains on collector:", e)
			continue
		}
		artifacts[i].MediaHash = digest
		if digest != "" {
			uploads[digest] = true
		}
	}
	return q.Update(func(s *State) error {
		index := -1
		for i, j := range s.Jobs {
			if j.Session.Run == job.Session.Run {
				index = i
				break
			}
		}
		if index < 0 {
			return nil
		}
		for _, a := range artifacts {
			if uploads[a.MediaHash] {
				if s.MediaPending == nil {
					s.MediaPending = map[string]bool{}
				}
				s.MediaPending[a.MediaHash] = true
			}
			ev := s.event(job.Session, "artifact", job.At)
			ev.Artifact = &a
			s.append(ev)
		}

		s.Jobs = append(s.Jobs[:index], s.Jobs[index+1:]...)
		return nil
	})
}
