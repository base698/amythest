package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lifecycle contains only identity and state. Conversation and tool payloads are
// deliberately not part of the contract consumed by collectors.
type Lifecycle struct {
	Agent    string    `json:"agent,omitempty"`
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Phase    string    `json:"phase"`
	Provider string    `json:"session_id"`
	Turn     string    `json:"turn_id"`
	Pane     string    `json:"pane"`
	Project  string    `json:"project"`
	Platform string    `json:"platform"`
	Dir      string    `json:"cwd"`
	Outcome  string    `json:"outcome"`
}

func (h Lifecycle) validate() error {
	if h.Agent != "" && h.Agent != "hermes" && h.Agent != "codex" {
		return errors.New("unknown lifecycle agent")
	}
	if h.ID == "" || len(h.ID) > 100 || h.Provider == "" || len(h.Provider) > 200 || len(h.Turn) > 200 || h.At.IsZero() || len(h.Project) > 200 || len(h.Platform) > 100 || len(h.Dir) > 4096 {
		return errors.New("invalid lifecycle metadata")
	}
	switch h.Phase {
	case "session", "start", "finish", "blocked", "working", "close":
	default:
		return errors.New("unknown lifecycle phase")
	}
	if h.Phase != "session" && h.Phase != "close" && h.Turn == "" {
		return errors.New("turn identity required")
	}
	return nil
}
func hookKey(provider, turn string) string {
	h := sha256.Sum256([]byte(provider + "\x00" + turn))
	return hex.EncodeToString(h[:16])
}
func (q *Queue) Lifecycle(h Lifecycle) error {
	if h.Agent == "" {
		h.Agent = "hermes"
	}
	// Preserve existing Hermes receipts, namespace new provider kinds.
	providerKey := h.Provider
	if h.Agent != "hermes" {
		providerKey = h.Agent + ":" + h.Provider
	}

	if e := h.validate(); e != nil {
		return e
	}
	return q.Update(func(s *State) error {
		if s.HookEvents == nil {
			s.HookEvents = map[string]bool{}
		}
		if s.HookFinished == nil {
			s.HookFinished = map[string]bool{}
		}
		if s.HookEvents[h.ID] {
			return nil
		}
		key := h.Agent + ":" + hookKey(h.Provider, "")
		for k, p := range s.Sessions {
			if p.Pane.Agent == h.Agent && p.Pane.Provider == h.Provider {
				key = k
				break
			}
		}
		p, ok := s.Sessions[key]
		if !ok {
			p = Session{ID: ID(), Pane: Pane{ID: key, Provider: h.Provider, Agent: h.Agent, Project: h.Project, Tab: h.Platform, Dir: h.Dir, Status: "idle"}, Seen: map[string]bool{}}
			s.append(Event{At: h.At, Kind: "slot.seen", Session: p.ID, Pane: key, Provider: h.Provider, Agent: h.Agent, Project: h.Project, Tab: h.Platform})
		}
		p.Hooked = true
		finished := s.HookFinished[hookKey(providerKey, h.Turn)]
		switch h.Phase {
		case "start":
			if !finished && p.Turn != h.Turn {
				// If capture began from herdr status, adopt its run instead of creating a
				// second one. A distinct explicit turn supersedes an unresolved old turn.
				if p.Run != "" && p.Turn != "" {
					ev := s.event(p, "run.finish", h.At)
					ev.Outcome = "unknown"
					s.append(ev)
					s.HookFinished[hookKey(providerKey, p.Turn)] = true
					p.Run = ""
				}
				if p.Run == "" {
					p.Run = ID()
					p.Started = h.At
					p.RunBinding = p.Binding
					s.append(s.event(p, "run.start", h.At))
				}
				p.Turn = h.Turn
				p.Pane.Status = "working"
				ev := s.event(p, "slot.status", h.At)
				ev.Status = "working"
				s.append(ev)
			}
		case "finish":
			if !finished && (p.Turn == h.Turn || p.Turn == "") {
				if p.Run != "" {
					ev := s.event(p, "run.finish", h.At)
					switch h.Outcome {
					case "success", "failure", "cancelled":
						ev.Outcome = h.Outcome
					default:
						ev.Outcome = "unknown"
					}
					s.append(ev)
					if p.Pane.Dir != "" {
						s.Jobs = append(s.Jobs, Job{Session: p, Outcome: ev.Outcome, At: h.At})
					}
					p.Run = ""
				}
				s.HookFinished[hookKey(providerKey, h.Turn)] = true
				p.Turn = h.Turn
				p.Pane.Status = "idle"
				ev := s.event(p, "slot.status", h.At)
				ev.Status = "idle"
				s.append(ev)
			}
		case "blocked", "working":
			if !finished && p.Turn == h.Turn && p.Run != "" {
				p.Pane.Status = h.Phase
				ev := s.event(p, "slot.status", h.At)
				ev.Status = h.Phase
				s.append(ev)
			}
		case "close":
			if p.Run != "" {
				ev := s.event(p, "run.finish", h.At)
				ev.Outcome = "unknown"
				s.append(ev)
				s.HookFinished[hookKey(providerKey, p.Turn)] = true
				p.Run = ""
			}
			p.Pane.Status = "gone"
			s.append(s.event(p, "slot.gone", h.At))
		}
		s.Sessions[key] = p
		s.HookEvents[h.ID] = true
		return nil
	})
}

// DrainLifecycle deletes a spool entry only after events and its receipt have
// committed together. A crash between commit and deletion is a harmless replay.
func (q *Queue) DrainLifecycle(dir string) error {
	files, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		info, e := f.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Size() > 16384 {
			return errors.New("invalid lifecycle spool file")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var h Lifecycle
		if e = json.Unmarshal(b, &h); e != nil {
			return e
		}
		if e = q.Lifecycle(h); e != nil {
			return e
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	}
	return nil
}
