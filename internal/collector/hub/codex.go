package hub

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// CodexWatch is explicitly opted in per chat; it never scans other transcripts.
// The rollout format is a local Codex implementation detail, tested with 0.166.
// Only lifecycle records are projected; prompts, tools and response bodies are
// discarded. Native hooks can feed the same Lifecycle contract in future.
type CodexWatch struct {
	Session string `json:"session_id"`
	Path    string `json:"path"`
	Project string `json:"project"`
	Title   string `json:"title"`
}
type codexRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Turn string `json:"turn_id"`
	} `json:"payload"`
}

func (q *Queue) PollCodex(config string) error {
	b, e := os.ReadFile(config)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if len(b) > 65536 {
		return errors.New("Codex watch config too large")
	}
	var watches []CodexWatch
	if e = json.Unmarshal(b, &watches); e != nil {
		return e
	}
	for _, w := range watches {
		if w.Session == "" || len(w.Session) > 200 || !filepath.IsAbs(w.Path) || len(w.Project) > 200 || len(w.Title) > 100 {
			return errors.New("invalid Codex watch")
		}
		if e = q.pollCodex(w); e != nil {
			return fmt.Errorf("Codex lifecycle: %w", e)
		}
	}
	return nil
}
func readCodexLine(r *bufio.Reader) ([]byte, error) {
	// A partial record must not advance the checkpoint. Bound memory even for a
	// corrupt file, and never include record contents in diagnostics.
	var line []byte
	for {
		part, e := r.ReadSlice('\n')
		if len(line)+len(part) > 64<<20 {
			return nil, errors.New("Codex record exceeds size limit")
		}
		line = append(line, part...)
		if e != bufio.ErrBufferFull {
			return line, e
		}
	}
}
func (q *Queue) pollCodex(w CodexWatch) error {
	f, e := os.Open(w.Path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	r := bufio.NewReader(f)
	first, e := readCodexLine(r)
	if e == io.EOF {
		return nil
	}
	if e != nil {
		return e
	}
	var meta codexRecord
	if json.Unmarshal(first, &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID != w.Session {
		return errors.New("Codex watch session does not match file")
	}
	key := hookKey(w.Session, w.Path)
	offset := q.Snapshot().CodexOffsets[key]
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if offset > info.Size() {
		offset = 0
	} // replay is inert after truncate/restore
	if offset == 0 {
		offset = int64(len(first))
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return e
	}
	r.Reset(f)
	initial := offset
	for count := 0; count < 1000; count++ {
		line, e := readCodexLine(r)
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		var record codexRecord
		if json.Unmarshal(line, &record) != nil {
			return errors.New("invalid Codex record")
		}
		if record.Type == "event_msg" {
			phase, outcome := "", ""
			switch record.Payload.Type {
			case "task_started":
				phase = "start"
			case "task_complete":
				phase = "finish"
				outcome = "success"
			case "turn_aborted":
				phase = "finish"
				outcome = "cancelled"
			}
			if phase != "" {
				if record.Payload.Turn == "" {
					return errors.New("Codex lifecycle missing turn identity")
				}
				h := Lifecycle{Agent: "codex", ID: "codex:" + hookKey(w.Session, record.Payload.Turn+":"+record.Payload.Type), At: record.Timestamp, Phase: phase, Provider: w.Session, Turn: record.Payload.Turn, Project: w.Project, Platform: w.Title, Outcome: outcome}
				if e = q.Lifecycle(h); e != nil {
					return e
				}
			}
		}
		offset += int64(len(line))
	}
	if offset == initial {
		return nil
	}
	// Events and their durable receipts precede the checkpoint. A crash before
	// this save replays safely instead of losing an event.
	return q.Update(func(s *State) error {
		if s.CodexOffsets == nil {
			s.CodexOffsets = map[string]int64{}
		}
		s.CodexOffsets[key] = offset
		return nil
	})
}
