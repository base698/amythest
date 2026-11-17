// Package find is one search over everything connected to the work: what
// agents said and produced, the vault's notes, cards and tasks, and the text
// on screen in open terminals. A result that came from a pane still open on
// this machine jumps to that pane; the rest open in the browser.
package find

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/apiclient"
)

// API is what search reads from the server.
type API interface {
	WorkOverview(ctx context.Context) (*apiclient.WorkSnapshot, error)
	WorkRun(ctx context.Context, run string) (*apiclient.WorkSnapshot, error)
	WorkSearch(ctx context.Context, query string, limit int) ([]apiclient.WorkHit, error)
	VaultSearch(ctx context.Context, query string) ([]apiclient.VaultHit, error)
	Endpoint() string
}

// Runner runs a program and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Result is one line of the result list.
type Result struct {
	Group   string // "agents", "vault", "tickets" or "screen"
	Kind    string // chat, commit, image, note, card, task, screen, …
	Title   string
	Snippet string
	Where   string // project / tab, or what the note is
	When    time.Time
	Run     string // agents: the run it belongs to
	Project string // agents: the Work project it is filed under
	Ref     string // agents: a commit's id
	Pane    string // screen, or agents once located: the pane to go to
	URL     string // where it opens in a browser, if anywhere
}

// Viewable reports whether the result is a thing an agent made (a commit, an
// image, a file) rather than something said: it can be shown itself.
func (r Result) Viewable() bool {
	return r.Group == "agents" && r.Kind != "chat" && r.Kind != "observation" && r.Project != ""
}

// Pane ids tell the two herdr runtimes apart: terminal herdr's are
// "w<workspace>:p<pane>", the Herdr App's are terminal addresses like "t1z".
func hostOf(pane string) string {
	if strings.Contains(pane, ":") {
		return "herdr"
	}
	return "app"
}

// Places maps runs to the panes they ran in, from the server's overview.
type Places struct {
	runSession map[string]string
	slot       map[string]apiclient.WorkSlot // by session
	session    map[string]apiclient.WorkSession
	local      string // this machine's collector id; empty if unknown
}

// NewPlaces indexes an overview. localCollector is this machine's collector
// id, so a pane on another machine is never mistaken for one here.
func NewPlaces(o *apiclient.WorkSnapshot, localCollector string) *Places {
	p := &Places{runSession: map[string]string{}, slot: map[string]apiclient.WorkSlot{}, session: map[string]apiclient.WorkSession{}, local: localCollector}
	if o != nil {
		p.add(o)
	}
	return p
}

func (p *Places) add(o *apiclient.WorkSnapshot) {
	for _, r := range o.Runs {
		p.runSession[r.ID] = r.Session
	}
	for _, s := range o.Slots {
		if s.Session != "" {
			p.slot[s.Session] = s
		}
	}
	for _, s := range o.Sessions {
		p.session[s.ID] = s
	}
}

// Known reports whether the run's session has been looked up.
func (p *Places) Known(run string) bool { _, ok := p.runSession[run]; return ok }

// Pane returns the open pane on this machine where a run happened, and the
// tab it is shown as. ok is false when the session has ended, ran on another
// machine, or is not known.
func (p *Places) Pane(run string) (pane, tab string, ok bool) {
	s, found := p.slot[p.runSession[run]]
	if !found || s.Pane == "" || s.Status == "gone" {
		return "", "", false
	}
	if p.local == "" || s.Collector != p.local {
		return "", s.Tab, false
	}
	return s.Pane, s.Tab, true
}

// Tab is the tab name a run's session goes by, open or not.
func (p *Places) Tab(run string) string {
	id := p.runSession[run]
	if s, ok := p.slot[id]; ok && s.Tab != "" {
		return s.Tab
	}
	return p.session[id].Title
}

// LocalCollector reads this machine's collector id from the collector's
// credential file. It is empty when no collector is registered here.
func LocalCollector() string {
	dir := os.Getenv("AMYTHEST_COLLECTOR_STATE")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state", "amythest-collector")
	}
	b, err := os.ReadFile(filepath.Join(dir, "credential.json"))
	if err != nil {
		return ""
	}
	var v struct {
		Credential struct {
			Client string `json:"client_id"`
		} `json:"credential"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return v.Credential.Client
}

// fromWork turns server hits into results.
func fromWork(hits []apiclient.WorkHit, p *Places, endpoint string) []Result {
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		r := Result{Group: "agents", Kind: h.Kind, Title: oneLine(h.Title), Run: h.Run, Project: h.Project, URL: strings.TrimRight(endpoint, "/") + "/kanban/focus"}
		// What was said is worth quoting; an artifact's body is its path or id.
		switch h.Kind {
		case "chat", "observation":
			r.Snippet = oneLine(h.Body)
		case "commit":
			if id := oneLine(h.Body); len(id) >= 7 && strings.Trim(id, "0123456789abcdef") == "" {
				r.Ref = id
			}
		}
		r.When, _ = time.Parse(time.RFC3339Nano, h.Created)
		tab := ""
		if p != nil {
			if pane, t, ok := p.Pane(h.Run); ok {
				r.Pane = pane
				tab = t
			} else {
				tab = p.Tab(h.Run)
			}
		}
		r.Where = strings.Trim(h.Project+" / "+tab, " /")
		if r.Title == "" {
			r.Title, r.Snippet = oneLine(h.Body), ""
		}
		out = append(out, r)
	}
	return out
}

// The server marks matches in a vault excerpt as HTML.
var plainExcerpt = strings.NewReplacer("<b>", "", "</b>", "", "\x02", "", "\x03", "")

func fromVault(hits []apiclient.VaultHit, endpoint string) []Result {
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		kind := h.Kind
		if kind == "" {
			kind = "note"
		}
		r := Result{Group: "vault", Kind: kind, Title: oneLine(h.Title), Snippet: oneLine(html.UnescapeString(plainExcerpt.Replace(h.Excerpt))), Where: h.Slug}
		r.When, _ = time.ParseInLocation("2006-01-02", h.Date, time.Local)
		if h.Archived {
			r.Where += " (archived)"
		}
		// Only links under the notes mount are followed.
		target := h.URL
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "/") || strings.Contains(target, "..") {
			target = h.Slug
		}
		r.URL = strings.TrimRight(endpoint, "/") + "/" + target
		out = append(out, r)
	}
	// Things to do before things to read.
	rank := map[string]int{"task": 0, "card": 1}
	sort.SliceStable(out, func(i, j int) bool {
		a, ok := rank[out[i].Kind]
		if !ok {
			a = 2
		}
		b, ok := rank[out[j].Kind]
		if !ok {
			b = 2
		}
		return a < b
	})
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// --- tickets from external trackers ----------------------------------------

// Ticket is an item from an external tracker (Jira, Azure Boards, …), as the
// terminal client's ticket sources list them.
type Ticket struct {
	Source string
	ID     string
	Title  string
	Meta   string // id, state and the like
	URL    string
}

// searchTickets finds tickets whose id, title or details contain every word.
func searchTickets(tickets []Ticket, query string, limit int) []Result {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	var out []Result
	for _, t := range tickets {
		text := strings.ToLower(t.ID + " " + t.Title + " " + t.Meta)
		match := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		where := t.Meta
		if where == "" {
			where = t.ID
		}
		out = append(out, Result{Group: "tickets", Kind: t.Source, Title: oneLine(t.Title), Where: where, URL: t.URL})
		if len(out) == limit {
			break
		}
	}
	return out
}

// --- text on screen in open terminals --------------------------------------

// Screen is the text of one open terminal, read when search opened.
type Screen struct {
	Pane  string
	Where string
	Lines []string
}

// ReadScreens reads the recent text of every open terminal in the Herdr App
// (and its place in the window). Reading is local and nothing is stored.
func ReadScreens(run Runner, history int) []Screen {
	if run == nil {
		run = execRunner
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out, err := run(ctx, "hc", "ls", "--json")
	if err != nil {
		return nil
	}
	var ls struct {
		Terminals []struct {
			Address string `json:"address"`
			Where   string `json:"where"`
			State   string `json:"state"`
		} `json:"terminals"`
	}
	if json.Unmarshal(out, &ls) != nil {
		return nil
	}
	screens := make([]Screen, len(ls.Terminals))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 6)
	for i, t := range ls.Terminals {
		// A terminal on another machine or not shown in the window is skipped.
		if t.State != "live" || t.Where == "" || strings.Contains(t.Address, "/") {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			text, err := run(ctx, "hc", "read", t.Address, "--lines", itoa(history))
			if err != nil {
				return
			}
			screens[i] = Screen{Pane: t.Address, Where: t.Where, Lines: strings.Split(string(text), "\n")}
		}()
	}
	wg.Wait()
	kept := screens[:0]
	for _, s := range screens {
		if s.Pane != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

// searchScreens finds lines containing every word of the query, newest (the
// lowest on screen) first, at most perPane from each terminal.
func searchScreens(screens []Screen, query string, perPane, limit int) []Result {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	var out []Result
	for _, s := range screens {
		found := 0
		for i := len(s.Lines) - 1; i >= 0 && found < perPane; i-- {
			line := oneLine(s.Lines[i])
			low := strings.ToLower(line)
			match := line != ""
			for _, w := range words {
				if !strings.Contains(low, w) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			found++
			out = append(out, Result{Group: "screen", Kind: "screen", Title: line, Where: s.Where, Pane: s.Pane})
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// --- going there -----------------------------------------------------------

// Focus brings a pane to the front in whichever herdr it belongs to.
func Focus(pane string, run Runner) error {
	if run == nil {
		run = execRunner
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if hostOf(pane) == "herdr" {
		bin := os.Getenv("HERDR_BIN_PATH")
		if bin == "" {
			bin = "herdr"
		}
		// Inside terminal herdr the search is a popup, and herdr gives focus
		// back to where it was when the popup closes. Ask a moment after this
		// process (and so the popup) has ended.
		if os.Getenv("HERDR_ENV") == "1" {
			_, err := run(ctx, "sh", "-c", `(sleep 0.4; "$0" agent focus "$1") >/dev/null 2>&1 &`, bin, pane)
			return err
		}
		_, err := run(ctx, bin, "agent", "focus", pane)
		return err
	}
	_, err := run(ctx, "herdr-app", "do", "pane.focus", "--terminal", pane)
	return err
}
