package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func hcSocketPath() string {
	if p := os.Getenv("AMYTHEST_COLLECTOR_HC_SOCK"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr-core", "default", "default.sock")
}

// HCTerminal is the subset of herdr-core's Terminal amythest-collector uses.
type HCTerminal struct {
	Address   string            `json:"address"`
	Cwd       string            `json:"cwd"`
	Status    *HCStatus         `json:"status"`
	Turn      int               `json:"turn"`
	Title     string            `json:"title"`
	Name      *string           `json:"name"`
	Parent    *string           `json:"parent"`
	Labels    map[string]string `json:"labels"`
	State     string            `json:"state"`
	CreatedMs int64             `json:"created_ms"`
	Fg        *struct {
		Name string   `json:"name"`
		Cwd  string   `json:"cwd"`
		Pid  int      `json:"pid"`
		Argv []string `json:"argv"`
	} `json:"foreground"`
}

// HCStatus is what the program in a terminal reports about itself. Terminals
// running a plain shell have none.
type HCStatus struct {
	App   string `json:"app"`
	State string `json:"state"`
}

// UnmarshalJSON accepts herdr-core's {app, state} object and also a bare state
// string, the form `hc ls` prints, so either source decodes.
func (s *HCStatus) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.State)
	}
	type plain HCStatus
	return json.Unmarshal(b, (*plain)(s))
}

func (t HCTerminal) StatusString() string {
	if t.Status == nil {
		return ""
	}
	return t.Status.State
}

func (t HCTerminal) Dir() string {
	if t.Fg != nil && t.Fg.Cwd != "" {
		return t.Fg.Cwd
	}
	return t.Cwd
}

// HCEvent is one `event` notification from herdr-core.
type HCEvent struct {
	Seq      uint64          `json:"seq"`
	Type     string          `json:"type"`
	Terminal string          `json:"terminal"`
	Data     json.RawMessage `json:"data"`
}

// HCSubscribe subscribes to every terminal's fact changes. It returns the
// initial snapshot and a channel of events (closed when the connection drops).
func HCSubscribe() ([]HCTerminal, <-chan HCEvent, func(), error) {
	conn, err := net.DialTimeout("unix", hcSocketPath(), 10*time.Second)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("hc: dial: %w", err)
	}
	// A core that accepts the connection but never answers must not hang the caller.
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	req := `{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{}}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var snap struct {
		Result *struct {
			Seq       uint64       `json:"seq"`
			Terminals []HCTerminal `json:"terminals"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	var decodeErr error
	for sc.Scan() {
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &probe) != nil || (len(probe.Result) == 0 && len(probe.Error) == 0) {
			continue // not the reply to our request
		}
		// This is the reply. A snapshot that does not decode is an error to
		// report, not a line to skip: skipping would wait forever for another.
		decodeErr = json.Unmarshal(sc.Bytes(), &snap)
		break
	}
	if decodeErr != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("hc: decode snapshot: %w", decodeErr)
	}
	if snap.Result == nil {
		err := sc.Err()
		conn.Close()
		if len(snap.Error) == 0 && err != nil {
			return nil, nil, nil, fmt.Errorf("hc: subscribe: %w", err)
		}
		return nil, nil, nil, fmt.Errorf("hc: subscribe failed: %s", snap.Error)
	}
	conn.SetDeadline(time.Time{}) // events may be hours apart
	baseSeq := snap.Result.Seq
	ch := make(chan HCEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		for sc.Scan() {
			var n struct {
				Method string  `json:"method"`
				Params HCEvent `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &n) != nil || n.Method != "event" {
				continue
			}
			if n.Params.Seq <= baseSeq && n.Params.Type != "missed" {
				continue
			}
			select {
			case ch <- n.Params:
			case <-done: // the caller stopped reading; do not block forever on a full channel
				return
			}
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { close(done); conn.Close() }) }
	return snap.Result.Terminals, ch, stop, nil
}
