package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	Address string            `json:"address"`
	Cwd     string            `json:"cwd"`
	Status  *string           `json:"status"`
	Turn    int               `json:"turn"`
	Title   string            `json:"title"`
	Name    *string           `json:"name"`
	Labels  map[string]string `json:"labels"`
	State   string            `json:"state"`
	Fg      *struct {
		Name string `json:"name"`
		Cwd  string `json:"cwd"`
	} `json:"foreground"`
}

func (t HCTerminal) StatusString() string {
	if t.Status == nil {
		return ""
	}
	return *t.Status
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
	conn, err := net.Dial("unix", hcSocketPath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("hc: dial: %w", err)
	}
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
	for sc.Scan() {
		if json.Unmarshal(sc.Bytes(), &snap) == nil && (snap.Result != nil || len(snap.Error) > 0) {
			break
		}
	}
	if snap.Result == nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("hc: subscribe failed: %s", snap.Error)
	}
	baseSeq := snap.Result.Seq
	ch := make(chan HCEvent, 64)
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
			ch <- n.Params
		}
	}()
	return snap.Result.Terminals, ch, func() { conn.Close() }, nil
}
