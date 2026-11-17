package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/base698/amythest/internal/collector/herdr"
	"github.com/base698/amythest/internal/collector/hub"
)

// A machine can run two herdr runtimes at once: the terminal herdr server and
// herdr-core, which backs the Herdr App. The queue needs one complete pane
// list per inventory, because a pane missing from it is recorded as gone and
// its run as abandoned. topology merges both runtimes into that one list.
//
// A runtime that stops answering keeps its last panes. Only a runtime that is
// plainly not running (its socket is missing or refuses connections) counts as
// having no panes, and only after absentGrace, so a restart or an update of
// either runtime never abandons live runs.
const (
	absentGrace      = 30 * time.Second
	inventoryRefresh = 30 * time.Second
)

type inventoryQueue interface {
	Inventory(panes []hub.Pane, at time.Time) error
}

type runtimeState struct {
	panes    []hub.Pane
	reported bool      // has delivered a complete list at least once
	absent   time.Time // when it was first found not running; zero while reachable
}

type topology struct {
	mu      sync.Mutex
	q       inventoryQueue
	now     func() time.Time
	order   []string
	sources map[string]*runtimeState
	last    string
	lastAt  time.Time
}

func newTopology(q inventoryQueue, names ...string) *topology {
	t := &topology{q: q, now: time.Now, order: names, sources: map[string]*runtimeState{}}
	for _, n := range names {
		t.sources[n] = &runtimeState{}
	}
	return t
}

// report records one runtime's complete pane list.
func (t *topology) report(name string, panes []hub.Pane) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sources[name]
	s.panes, s.reported, s.absent = panes, true, time.Time{}
	return t.flush()
}

// unreachable records a failed read of one runtime. notRunning is true only
// when the failure shows the runtime is not there at all; any other failure
// says nothing about its panes, so they are kept as last reported.
func (t *topology) unreachable(name string, notRunning bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sources[name]
	if !notRunning {
		s.absent = time.Time{}
		return nil
	}
	if s.absent.IsZero() {
		s.absent = t.now()
	}
	return t.flush()
}

// flush delivers the merged inventory once every runtime is accounted for:
// either it has reported, or it has been not running for absentGrace.
func (t *topology) flush() error {
	now := t.now()
	all := []hub.Pane{}
	for _, name := range t.order {
		s := t.sources[name]
		switch {
		case !s.absent.IsZero() && now.Sub(s.absent) >= absentGrace:
			// not running: it has no panes
		case s.reported:
			all = append(all, s.panes...)
		default:
			return nil // may still be starting; an inventory now would be incomplete
		}
	}
	print := fmt.Sprintf("%q", all)
	if print == t.last && now.Sub(t.lastAt) < inventoryRefresh {
		return nil
	}
	if e := t.q.Inventory(all, now); e != nil {
		return e
	}
	t.last, t.lastAt = print, now
	return nil
}

// notRunning reports whether a socket error means nothing is listening.
func notRunning(e error) bool {
	return errors.Is(e, syscall.ENOENT) || errors.Is(e, syscall.ECONNREFUSED)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// captureHC inventories the terminals of herdr-core (the Herdr App). Any event
// (a status or turn change, a label edit, a terminal starting or ending) is
// answered by reading a fresh complete snapshot rather than patching the last.
func captureHC(ctx context.Context, topo *topology, skipHermes bool) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		terms, events, stop, e := herdr.HCSubscribe()
		if e != nil {
			if e = topo.unreachable("hc", notRunning(e)); e != nil {
				return e
			}
			if !sleepCtx(ctx, 5*time.Second) {
				return nil
			}
			continue
		}
		if e = topo.report("hc", hcPanes(terms, claudeSession, skipHermes)); e != nil {
			stop()
			return e
		}
		select {
		case <-ctx.Done():
			stop()
			return nil
		case <-time.After(inventoryRefresh):
		case <-events:
			sleepCtx(ctx, 300*time.Millisecond) // let a burst of changes settle
		}
		stop()
	}
}

// hcPanes maps herdr-core terminals to queue panes. Only live terminals whose
// program reports a status are agents; plain shells are left out.
func hcPanes(terms []herdr.HCTerminal, session func(herdr.HCTerminal) string, skipHermes bool) []hub.Pane {
	byAddr := map[string]herdr.HCTerminal{}
	for _, t := range terms {
		byAddr[t.Address] = t
	}
	panes := []hub.Pane{}
	for _, t := range terms {
		if t.State != "live" || t.Status == nil || t.Status.App == "" {
			continue
		}
		// machine/t7 is a terminal on another machine: its folder is not on
		// this disk, and that machine's own collector reports it.
		if strings.Contains(t.Address, "/") {
			continue
		}
		agent := hcAgent(t.Status.App)
		// Codex sessions are read from Codex's own session records. Without a
		// Codex session id here the two could not be recognized as one session.
		if agent == "codex" || skipHermes && agent == "hermes" {
			continue
		}
		// A helper started by an agent has no place in the window; file it
		// under the space and tab of the terminal that started it.
		where := t.Labels["where"]
		for p, hops := t, 0; where == "" && p.Parent != nil && hops < 8; hops++ {
			next, ok := byAddr[*p.Parent]
			if !ok {
				break
			}
			p, where = next, next.Labels["where"]
		}
		project, tab := splitWhere(where)
		status := t.Status.State
		switch status {
		case "working", "blocked", "idle", "done":
		default:
			status = "idle"
		}
		provider := ""
		if session != nil {
			provider = session(t)
		}
		if provider == "" {
			// Still identifies one incarnation: an address is only reused by a
			// terminal created later.
			provider = fmt.Sprintf("hc:%s@%d", t.Address, t.CreatedMs)
		}
		panes = append(panes, hub.Pane{ID: t.Address, Provider: provider, Agent: agent, Project: project, Tab: tab, Dir: t.Dir(), Status: status})
	}
	return panes
}

// hcAgent maps herdr-core's app name to the agent name terminal herdr uses, so
// one agent has one name whichever runtime hosts it.
func hcAgent(app string) string {
	if app == "claude-code" {
		return "claude"
	}
	return app
}

// claudeSession finds the Claude Code session running in a terminal. Claude
// records each running process in <config>/sessions/<pid>.json; a session
// started with --resume also names it on its command line.
func claudeSession(t herdr.HCTerminal) string {
	if t.Status == nil || t.Status.App != "claude-code" || t.Fg == nil {
		return ""
	}
	if t.Fg.Pid > 0 {
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".claude")
		}
		if b, e := os.ReadFile(filepath.Join(dir, "sessions", strconv.Itoa(t.Fg.Pid)+".json")); e == nil && len(b) < 1<<20 {
			var v struct {
				Pid     int    `json:"pid"`
				Session string `json:"sessionId"`
			}
			if json.Unmarshal(b, &v) == nil && v.Pid == t.Fg.Pid && v.Session != "" {
				return v.Session
			}
		}
	}
	for i, a := range t.Fg.Argv {
		if (a == "--resume" || a == "-r") && i+1 < len(t.Fg.Argv) && !strings.HasPrefix(t.Fg.Argv[i+1], "-") {
			return t.Fg.Argv[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--resume="); ok && v != "" {
			return v
		}
	}
	return ""
}
