// Package store appends amythest-collector records to JSONL files under the state dir.
// The spike keeps everything append-only; SQLite comes in phase 1.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

func Dir() string {
	if d := os.Getenv("AMYTHEST_COLLECTOR_STATE"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "amythest-collector")
}

var mu sync.Mutex

// Append writes one JSON record to <state>/<name>.jsonl.
func Append(name string, rec any) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(Dir(), name+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Seen returns the set of artifact keys already recorded.
func Seen() map[string]bool {
	seen := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(Dir(), "artifacts.jsonl"))
	if err != nil {
		return seen
	}
	for _, line := range splitLines(b) {
		var r struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(line, &r) == nil && r.Key != "" {
			seen[r.Key] = true
		}
	}
	return seen
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
