// Command amythest-collector — phase 0 spike.
//
//	amythest-collector panes   [-machines local,remote-host]   pane → project/work item/agent/repo map
//	amythest-collector watch   [-machines ...] [-hc]          live agent events; collects artifacts on done
//	amythest-collector collect <pane> [-since 24h] [-record]  run the artifact collector for one pane now
//	amythest-collector hook                                   Claude Code hook: reads hook JSON on stdin
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/collector/collect"
	"github.com/base698/amythest/internal/collector/herdr"
	"github.com/base698/amythest/internal/collector/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "register":
		if err := register(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "sync":
		if err := syncWork(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "panes":
		cmdPanes(args)
	case "watch":
		cmdWatch(args)
	case "collect":
		cmdCollect(args)
	case "hook":
		cmdHook()
	case "show":
		cmdShow(args)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: amythest-collector register|sync|panes|watch|collect|hook  (see source header)")
	os.Exit(2)
}

func parseMachines(s string) []herdr.Machine {
	var ms []herdr.Machine
	for _, n := range strings.Split(s, ",") {
		n = strings.TrimSpace(n)
		switch n {
		case "":
		case "local":
			ms = append(ms, herdr.Machine{Name: "local"})
		default:
			ms = append(ms, herdr.Machine{Name: n, Host: n})
		}
	}
	return ms
}

var numeric = regexp.MustCompile(`^\d+$`)

func ownerFor(m herdr.Machine, t *herdr.Topology, p herdr.Pane) collect.Owner {
	tab := t.Tabs[p.TabID]
	if tab == "" || numeric.MatchString(tab) {
		tab = "tab " + tab + " (unlabeled)"
	}
	return collect.Owner{Machine: m.Name, Project: t.Workspaces[p.WorkspaceID], WorkItem: tab,
		Pane: p.PaneID, Agent: p.Agent, Session: p.SessionID(), Attribute: "exact"}
}

// ---- panes ----

func cmdPanes(args []string) {
	fs := flag.NewFlagSet("panes", flag.ExitOnError)
	machines := fs.String("machines", "local", "comma-separated: local and/or ssh hosts")
	fs.Parse(args)
	for _, m := range parseMachines(*machines) {
		t, err := m.Snapshot()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		fmt.Printf("== %s\n", m.Name)
		for _, p := range t.Panes {
			if p.Agent == "" {
				continue
			}
			o := ownerFor(m, t, p)
			repo, branch := "", ""
			if m.Local() {
				repo, branch = collect.RepoInfo(p.Dir())
			}
			fmt.Printf("%-8s %-18s %-24s %-8s %-8s %s", p.PaneID, trunc(o.Project, 18), trunc(o.WorkItem, 24),
				p.Agent, p.Status, p.Dir())
			if repo != "" {
				fmt.Printf("  [%s]", branch)
			}
			if s := p.SessionID(); s != "" {
				fmt.Printf("  session=%s", trunc(s, 12))
				if collect.TranscriptPath(s) != "" {
					fmt.Print(" (transcript ✓)")
				}
			}
			fmt.Println()
		}
	}
}

func trunc(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// ---- watch ----

type paneState struct {
	status       string
	workingSince time.Time
}

type watcher struct {
	mu    sync.Mutex
	state map[string]*paneState // machine/pane -> state
	seen  map[string]bool
}

func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	machines := fs.String("machines", "local", "comma-separated: local and/or ssh hosts")
	withHC := fs.Bool("hc", false, "also watch herdr-core (Herdr App)")
	fs.Parse(args)
	w := &watcher{state: map[string]*paneState{}, seen: store.Seen()}
	var wg sync.WaitGroup
	for _, m := range parseMachines(*machines) {
		wg.Add(1)
		go func() { defer wg.Done(); w.watchTUI(m) }()
	}
	if *withHC {
		wg.Add(1)
		go func() { defer wg.Done(); w.watchHC() }()
	}
	wg.Wait()
}

func logf(format string, a ...any) {
	fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func (w *watcher) watchTUI(m herdr.Machine) {
	backoff := time.Second
	for {
		t, err := m.Snapshot()
		if err != nil {
			logf("%s: snapshot failed: %v (retry in %s)", m.Name, err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		var ids []string
		w.mu.Lock()
		for _, p := range t.Panes {
			ids = append(ids, p.PaneID)
			key := m.Name + "/" + p.PaneID
			if _, ok := w.state[key]; !ok {
				ps := &paneState{status: p.Status}
				if p.Status == "working" {
					ps.workingSince = time.Now()
				}
				w.state[key] = ps
			}
		}
		w.mu.Unlock()
		ch, stop, err := m.Subscribe(ids)
		if err != nil {
			logf("%s: subscribe failed: %v", m.Name, err)
			time.Sleep(5 * time.Second)
			continue
		}
		logf("%s: watching %d panes", m.Name, len(ids))
		resync := false
		for ev := range ch {
			switch ev.Event {
			case "pane_agent_status_changed", "pane.agent_status_changed":
				w.onTUIStatus(m, t, ev.Data)
			default:
				// topology changed: re-snapshot and resubscribe
				store.Append("events", map[string]any{"time": time.Now(), "machine": m.Name, "event": ev.Event, "data": ev.Data})
				logf("%s: %s → resync", m.Name, ev.Event)
				resync = true
			}
			if resync {
				break
			}
		}
		stop()
		if !resync {
			logf("%s: connection dropped; reconnecting", m.Name)
			time.Sleep(2 * time.Second)
		}
	}
}

func (w *watcher) onTUIStatus(m herdr.Machine, t *herdr.Topology, data json.RawMessage) {
	var d struct {
		PaneID string `json:"pane_id"`
		Status string `json:"agent_status"`
		Agent  string `json:"agent"`
	}
	json.Unmarshal(data, &d)
	var pane herdr.Pane
	for _, p := range t.Panes {
		if p.PaneID == d.PaneID {
			pane = p
		}
	}
	if pane.PaneID == "" {
		pane.PaneID = d.PaneID
	}
	if d.Agent != "" {
		pane.Agent = d.Agent
	}
	o := ownerFor(m, t, pane)
	key := m.Name + "/" + d.PaneID
	w.mu.Lock()
	ps := w.state[key]
	if ps == nil {
		ps = &paneState{}
		w.state[key] = ps
	}
	prev := ps.status
	ps.status = d.Status
	since := ps.workingSince
	if d.Status == "working" && prev != "working" {
		ps.workingSince = time.Now()
	}
	w.mu.Unlock()

	store.Append("events", map[string]any{"time": time.Now(), "machine": m.Name, "pane": d.PaneID,
		"project": o.Project, "work_item": o.WorkItem, "agent": pane.Agent, "from": prev, "to": d.Status})
	logf("%-10s %-16s %-22s %-7s %s → %s", m.Name, trunc(o.Project, 16), trunc(o.WorkItem, 22), pane.Agent, prev, d.Status)

	finished := prev == "working" && (d.Status == "done" || d.Status == "idle")
	if !finished {
		return
	}
	if !m.Local() {
		logf("           (collection on remote machines is not in the spike)")
		return
	}
	if since.IsZero() {
		since = time.Now().Add(-time.Hour)
	}
	go w.collectAndRecord(collect.Run{Owner: o, Dir: pane.Dir(), Since: since.Add(-time.Minute)})
}

func (w *watcher) collectAndRecord(r collect.Run) {
	arts := collect.Collect(r)
	n := 0
	w.mu.Lock()
	defer w.mu.Unlock()
	for k := range store.Seen() { // the hook writes too; pick up its records
		w.seen[k] = true
	}
	for _, a := range arts {
		if w.seen[a.Key] {
			continue
		}
		w.seen[a.Key] = true
		store.Append("artifacts", a)
		n++
		logf("           + %-6s %s  (%s, %s)", a.Kind, artifactLabel(a), a.Found, a.Owner.Attribute)
	}
	logf("           run summary: %s / %s — %d new artifact(s)", r.Owner.Project, r.Owner.WorkItem, n)
}

func artifactLabel(a collect.Artifact) string {
	switch a.Kind {
	case "commit":
		return a.Ref[:min(8, len(a.Ref))] + " " + a.Title
	case "pr":
		if a.Title != "" {
			return a.Ref + " " + a.Title
		}
	case "imageset":
		return fmt.Sprintf("%s  (%d images)", a.Ref, a.Count)
	}
	if a.Featured {
		return "★ " + a.Ref
	}
	return a.Ref
}

func (w *watcher) watchHC() {
	for {
		terms, ch, stop, err := herdr.HCSubscribe()
		if err != nil {
			logf("hc: %v (retry in 30s)", err)
			time.Sleep(30 * time.Second)
			continue
		}
		byAddr := map[string]herdr.HCTerminal{}
		for _, t := range terms {
			byAddr[t.Address] = t
		}
		workingSince := map[string]time.Time{}
		logf("hc: watching %d terminals", len(terms))
		for ev := range ch {
			if ev.Type == "created" {
				var t herdr.HCTerminal
				if json.Unmarshal(ev.Data, &t) == nil && t.Address != "" {
					byAddr[t.Address] = t
				}
			}
			t := byAddr[ev.Terminal]
			project, item := splitWhere(t.Labels["where"])
			store.Append("events", map[string]any{"time": time.Now(), "machine": "hc", "terminal": ev.Terminal,
				"project": project, "work_item": item, "type": ev.Type, "data": ev.Data})
			switch ev.Type {
			case "turn":
				var d struct {
					State string `json:"state"`
					Turn  int    `json:"turn"`
				}
				json.Unmarshal(ev.Data, &d)
				logf("%-10s %-16s %-22s %-7s turn %d %s", "hc", trunc(project, 16), trunc(item, 22), ev.Terminal, d.Turn, d.State)
				if d.State == "working" {
					workingSince[ev.Terminal] = time.Now()
				}
				if d.State == "done" {
					since := workingSince[ev.Terminal]
					if since.IsZero() {
						since = time.Now().Add(-time.Hour)
					}
					o := collect.Owner{Machine: "hc", Project: project, WorkItem: item, Pane: ev.Terminal, Attribute: "exact"}
					delete(workingSince, ev.Terminal)
					go w.collectAndRecord(collect.Run{Owner: o, Dir: t.Dir(), Since: since.Add(-10 * time.Second)})
				}
			case "status":
				var d struct {
					Status *struct {
						State string `json:"state"`
					} `json:"status"`
				}
				json.Unmarshal(ev.Data, &d)
				state := ""
				if d.Status != nil {
					state = d.Status.State
				}
				if state == "working" {
					if _, ok := workingSince[ev.Terminal]; !ok {
						workingSince[ev.Terminal] = time.Now()
					}
				}
				logf("%-10s %-16s %-22s %-7s status %s", "hc", trunc(project, 16), trunc(item, 22), ev.Terminal, state)
			case "finished", "created":
				logf("%-10s %-16s %-22s %-7s %s %s", "hc", trunc(project, 16), trunc(item, 22), ev.Terminal, ev.Type, trunc(string(ev.Data), 60))
			case "missed":
				logf("hc: missed events; resubscribing")
			}
			if ev.Type == "missed" {
				break
			}
		}
		stop()
		time.Sleep(time.Second)
	}
}

// splitWhere turns herdr-core's "space / tab" label into project and work item.
func splitWhere(where string) (string, string) {
	if i := strings.Index(where, " / "); i >= 0 {
		return where[:i], where[i+3:]
	}
	return where, ""
}

// ---- show ----

// cmdShow prints recorded artifacts grouped by project and work item, newest
// first. Paths and URLs are printed in full so the terminal can open them.
func cmdShow(args []string) {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	project := fs.String("project", "", "only this project (herdr workspace label, substring)")
	kind := fs.String("kind", "", "only this kind: commit, pr, image, video, imageset")
	n := fs.Int("n", 15, "items per work item")
	since := fs.Duration("since", 0, "only items newer than this (e.g. 24h)")
	fs.Parse(args)
	b, err := os.ReadFile(store.Dir() + "/artifacts.jsonl")
	if err != nil {
		fmt.Println("nothing recorded yet")
		return
	}
	type group struct {
		key   string
		items []collect.Artifact
	}
	groups := map[string]*group{}
	var order []string
	for _, line := range strings.Split(string(b), "\n") {
		var a collect.Artifact
		if json.Unmarshal([]byte(line), &a) != nil || a.Key == "" {
			continue
		}
		if *project != "" && !strings.Contains(a.Owner.Project, *project) {
			continue
		}
		if *kind != "" && a.Kind != *kind {
			continue
		}
		if *since > 0 && a.Time.Before(time.Now().Add(-*since)) {
			continue
		}
		k := a.Owner.Project + " / " + a.Owner.WorkItem
		if groups[k] == nil {
			groups[k] = &group{key: k}
			order = append(order, k)
		}
		groups[k].items = append(groups[k].items, a)
	}
	if len(order) == 0 {
		fmt.Println("no matching artifacts")
		return
	}
	for _, k := range order {
		g := groups[k]
		sort.Slice(g.items, func(i, j int) bool { return g.items[i].Time.After(g.items[j].Time) })
		counts := map[string]int{}
		for _, a := range g.items {
			counts[a.Kind]++
		}
		var parts []string
		for _, kd := range []string{"pr", "commit", "video", "image", "imageset", "file"} {
			if counts[kd] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[kd], kd))
			}
		}
		fmt.Printf("\n\033[1m%s\033[0m  (%s)\n", g.key, strings.Join(parts, ", "))
		for i, a := range g.items {
			if i == *n {
				fmt.Printf("  … %d more (-n to show more)\n", len(g.items)-*n)
				break
			}
			ref := a.Ref
			if a.Kind == "commit" && a.Repo != "" {
				ref = a.Ref[:min(8, len(a.Ref))] + "  " + a.Title
			} else if a.Kind == "imageset" {
				ref = fmt.Sprintf("%s  (%d images)", a.Ref, a.Count)
			}
			star := " "
			if a.Featured {
				star = "★"
			}
			fmt.Printf("  %s %s  %-8s %s\n", star, a.Time.Local().Format("01-02 15:04"), a.Kind, ref)
		}
	}
}

// ---- collect ----

func cmdCollect(args []string) {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	machine := fs.String("machine", "local", "machine (only local supported)")
	since := fs.Duration("since", 24*time.Hour, "look back this far")
	record := fs.Bool("record", false, "append new artifacts to artifacts.jsonl")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: amythest-collector collect <pane_id> [-since 24h] [-record]")
		os.Exit(2)
	}
	m := parseMachines(*machine)[0]
	t, err := m.Snapshot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var pane *herdr.Pane
	for i := range t.Panes {
		if t.Panes[i].PaneID == fs.Arg(0) {
			pane = &t.Panes[i]
		}
	}
	if pane == nil {
		fmt.Fprintln(os.Stderr, "no such pane:", fs.Arg(0))
		os.Exit(1)
	}
	r := collect.Run{Owner: ownerFor(m, t, *pane), Dir: pane.Dir(), Since: time.Now().Add(-*since)}
	fmt.Printf("%s / %s  (%s, %s)\n", r.Owner.Project, r.Owner.WorkItem, pane.Agent, r.Dir)
	seen := store.Seen()
	for _, a := range collect.Collect(r) {
		mark := " "
		if seen[a.Key] {
			mark = "="
		} else if *record {
			seen[a.Key] = true
			store.Append("artifacts", a)
			mark = "+"
		}
		fmt.Printf(" %s %-6s %-10s %-10s %s\n", mark, a.Kind, a.Found, a.Owner.Attribute, artifactLabel(a))
	}
}

// reorder moves flags ahead of positional args so `collect wB:p1 -since 2h` works.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && args[i] != "-record" {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			pos = append(pos, args[i])
		}
	}
	return append(flags, pos...)
}

// ---- hook ----

// cmdHook ingests a Claude Code PostToolUse hook payload. It must never fail
// the agent's tool call, so every error is swallowed and it always exits 0.
func cmdHook() {
	defer func() { recover(); os.Exit(0) }()
	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	var h struct {
		Session  string          `json:"session_id"`
		Cwd      string          `json:"cwd"`
		Event    string          `json:"hook_event_name"`
		Tool     string          `json:"tool_name"`
		Input    json.RawMessage `json:"tool_input"`
		Response json.RawMessage `json:"tool_response"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return
	}
	pane := os.Getenv("HERDR_PANE_ID")
	o := collect.Owner{Machine: "local", Pane: pane, Session: h.Session, Agent: "claude", Attribute: "exact"}
	var arts []collect.Artifact
	now := time.Now()
	switch h.Tool {
	case "Write", "Edit", "NotebookEdit":
		var in struct {
			Path string `json:"file_path"`
		}
		json.Unmarshal(h.Input, &in)
		if kind := mediaKind(in.Path); kind != "" {
			arts = append(arts, collect.Artifact{Key: "file:" + in.Path + "@" + now.UTC().Format(time.RFC3339),
				Kind: kind, Ref: in.Path, Found: "hook", Time: now, Owner: o})
		}
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		json.Unmarshal(h.Input, &in)
		if strings.Contains(in.Command, "gh pr create") {
			for _, u := range regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`).FindAllString(string(h.Response), -1) {
				arts = append(arts, collect.Artifact{Key: "pr:" + u, Kind: "pr", Ref: u, Found: "hook", Time: now, Owner: o})
			}
		}
		if strings.Contains(in.Command, "git ") && strings.Contains(in.Command, "commit") {
			arts = append(arts, hookCommits(h.Cwd, h.Response, now, o)...)
		}
	}
	if len(arts) == 0 {
		return
	}
	if pane != "" {
		if t, err := (herdr.Machine{Name: "local"}).Snapshot(); err == nil {
			for _, p := range t.Panes {
				if p.PaneID == pane {
					o = ownerFor(herdr.Machine{Name: "local"}, t, p)
					o.Session = h.Session
				}
			}
		}
	}
	for _, a := range arts {
		a.Owner = o
		store.Append("artifacts", a)
	}
}

// commitLine matches git's summary line: "[branch abc1234] subject" or
// "[branch (root-commit) abc1234] subject".
var commitLine = regexp.MustCompile(`\[([^\s\]]+)(?: \(root-commit\))? ([0-9a-f]{7,40})\] ([^\n\\]*)`)

// hookCommits turns `git commit` output into commit artifacts with exact
// (pane-level) attribution. The short id is resolved in the call's cwd.
func hookCommits(cwd string, resp json.RawMessage, now time.Time, o collect.Owner) []collect.Artifact {
	var out struct {
		Stdout string `json:"stdout"`
	}
	text := string(resp)
	if json.Unmarshal(resp, &out) == nil && out.Stdout != "" {
		text = out.Stdout
	}
	top, _ := collect.RepoInfo(cwd)
	var arts []collect.Artifact
	for _, m := range commitLine.FindAllStringSubmatch(text, -1) {
		sha := m[2]
		if top != "" {
			if b, err := exec.Command("git", "-C", top, "rev-parse", "--verify", "-q", sha+"^{commit}").Output(); err == nil {
				sha = strings.TrimSpace(string(b))
			}
		}
		arts = append(arts, collect.Artifact{Key: "commit:" + sha, Kind: "commit", Ref: sha, Title: m[3],
			Repo: top, Branch: m[1], Found: "hook", Time: now, Owner: o})
	}
	return arts
}

func mediaKind(p string) string {
	switch strings.ToLower(p[strings.LastIndex(p, ".")+1:]) {
	case "gif", "png", "jpg", "jpeg", "webp", "svg":
		return "image"
	case "mp4", "mov", "webm", "mkv":
		return "video"
	}
	return ""
}
