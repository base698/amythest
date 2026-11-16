// Package herdr talks to herdr runtimes: the herdr TUI server (line-delimited
// JSON over a Unix socket) and herdr-core (`hc`, JSON-RPC 2.0 over a Unix socket).
// Remote TUI servers are reached by running `nc -U` on the remote host over ssh.
package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Machine identifies a herdr TUI server. Host "" means the local Mac.
type Machine struct {
	Name string // label used in events ("local", "remote-host")
	Host string // ssh target; empty for local
}

func (m Machine) Local() bool { return m.Host == "" }

func tuiSocketPath() string {
	if p := os.Getenv("AMYTHEST_COLLECTOR_HERDR_SOCK"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

// dial opens a raw connection to the machine's herdr TUI socket.
func (m Machine) dial() (io.ReadWriteCloser, error) {
	if m.Local() {
		return net.DialTimeout("unix", tuiSocketPath(), 10*time.Second)
	}
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		m.Host, "exec nc -U ~/.config/herdr/herdr.sock")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &sshConn{cmd: cmd, w: stdin, r: stdout}, nil
}

type sshConn struct {
	cmd *exec.Cmd
	w   io.WriteCloser
	r   io.ReadCloser
}

func (c *sshConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *sshConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *sshConn) Close() error {
	c.w.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

var reqID atomic.Int64

type tuiResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// Call sends one request on a fresh connection and returns the result payload.
func (m Machine) Call(method string, params any, out any) error {
	conn, err := m.dial()
	if err != nil {
		return fmt.Errorf("%s: dial herdr: %w", m.Name, err)
	}
	defer conn.Close()
	timer := time.AfterFunc(15*time.Second, func() { conn.Close() })
	defer timer.Stop()
	if params == nil {
		params = struct{}{}
	}
	id := fmt.Sprintf("c%d", reqID.Add(1))
	req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r tuiResponse
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.ID != id {
			continue
		}
		if len(r.Error) > 0 && string(r.Error) != "null" {
			return fmt.Errorf("%s %s: %s", m.Name, method, r.Error)
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%s %s: connection closed without a response", m.Name, method)
}

// Pane is the subset of herdr's pane_info amythest-collector uses.
type Pane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Cwd         string `json:"cwd"`
	FgCwd       string `json:"foreground_cwd"`
	Agent       string `json:"agent"`
	Status      string `json:"agent_status"`
	Title       string `json:"terminal_title_stripped"`
	Session     *struct {
		Agent string `json:"agent"`
		Value string `json:"value"`
	} `json:"agent_session"`
}

func (p Pane) SessionID() string {
	if p.Session == nil {
		return ""
	}
	return p.Session.Value
}

func (p Pane) Dir() string {
	if p.FgCwd != "" {
		return p.FgCwd
	}
	return p.Cwd
}

type Workspace struct {
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
}

type Tab struct {
	ID    string `json:"tab_id"`
	Label string `json:"label"`
}

// Topology is a snapshot of panes plus workspace/tab labels.
type Topology struct {
	Panes      []Pane
	Workspaces map[string]string // id -> label
	Tabs       map[string]string // id -> label
}

func (m Machine) Snapshot() (*Topology, error) {
	var pl struct{ Panes []Pane }
	if err := m.Call("pane.list", map[string]any{}, &pl); err != nil {
		return nil, err
	}
	var wl struct{ Workspaces []Workspace }
	if err := m.Call("workspace.list", map[string]any{}, &wl); err != nil {
		return nil, err
	}
	t := &Topology{Panes: pl.Panes, Workspaces: map[string]string{}, Tabs: map[string]string{}}
	for _, w := range wl.Workspaces {
		t.Workspaces[w.ID] = w.Label
		var tl struct{ Tabs []Tab }
		if err := m.Call("tab.list", map[string]any{"workspace_id": w.ID}, &tl); err != nil {
			return nil, err
		}
		for _, tb := range tl.Tabs {
			t.Tabs[tb.ID] = tb.Label
		}
	}
	return t, nil
}

// TUIEvent is one subscription notification from a herdr TUI server.
type TUIEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// Subscribe opens a long-lived connection subscribed to agent status changes
// on the given panes plus pane lifecycle events. Events are delivered on the
// returned channel until the connection drops, at which point it is closed.
func (m Machine) Subscribe(paneIDs []string) (<-chan TUIEvent, func(), error) {
	conn, err := m.dial()
	if err != nil {
		return nil, nil, err
	}
	subs := []map[string]any{
		{"type": "pane.created"}, {"type": "pane.closed"}, {"type": "pane.agent_detected"},
		{"type": "tab.renamed"}, {"type": "workspace.renamed"},
	}
	for _, id := range paneIDs {
		subs = append(subs, map[string]any{"type": "pane.agent_status_changed", "pane_id": id})
	}
	req, _ := json.Marshal(map[string]any{"id": "sub", "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs}})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, nil, err
	}
	ch := make(chan TUIEvent, 64)
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); conn.Close() }) }
	go func() {
		defer close(ch)
		defer stop()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var ev TUIEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event != "" {
				select {
				case ch <- ev:
				case <-done:
					return
				}
				continue
			}
			var r tuiResponse
			if json.Unmarshal(sc.Bytes(), &r) == nil && len(r.Error) > 0 && string(r.Error) != "null" {
				fmt.Fprintf(os.Stderr, "%s: subscribe error: %s\n", m.Name, r.Error)
			}
		}
	}()
	return ch, stop, nil
}
