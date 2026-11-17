package wall

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
)

// Place is the herdr workspace this process was opened in.
type Place struct {
	Host    string   // "app" (Herdr App), "herdr" (terminal herdr) or ""
	Project string   // the workspace's name
	Panes   []string // every terminal in that workspace
}

// How long a new Herdr App terminal is given to be placed in its space.
var (
	placementTries = 8
	placementWait  = 250 * time.Millisecond
)

// Runner runs a program and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Locate works out the current workspace from the environment herdr gives
// every pane. It returns a zero Place outside herdr or when herdr cannot be
// asked; the caller then offers a project list.
func Locate(getenv func(string) string, run Runner) Place {
	if getenv == nil {
		getenv = os.Getenv
	}
	if run == nil {
		run = execRunner
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if id := getenv("HC_TERMINAL_ID"); id != "" {
		// A terminal the App has just opened is placed in its space a moment
		// after its program starts; ask again rather than give up at once.
		p := locateApp(ctx, id, run)
		for try := 0; p.Project == "" && try < placementTries; try++ {
			select {
			case <-ctx.Done():
				return p
			case <-time.After(placementWait):
			}
			p = locateApp(ctx, id, run)
		}
		return p
	}
	if ws := getenv("HERDR_WORKSPACE_ID"); ws != "" {
		bin := getenv("HERDR_BIN_PATH")
		if bin == "" {
			bin = "herdr"
		}
		return locateHerdr(ctx, bin, ws, run)
	}
	return Place{}
}

// The Herdr App labels each terminal "space / tab".
func locateApp(ctx context.Context, id string, run Runner) Place {
	out, err := run(ctx, "hc", "ls", "--json")
	if err != nil {
		return Place{}
	}
	var ls struct {
		Terminals []struct {
			Address string `json:"address"`
			Where   string `json:"where"`
		} `json:"terminals"`
	}
	if json.Unmarshal(out, &ls) != nil {
		return Place{}
	}
	space := ""
	for _, t := range ls.Terminals {
		if t.Address == id {
			space = spaceOf(t.Where)
		}
	}
	if space == "" {
		return Place{Host: "app"}
	}
	p := Place{Host: "app", Project: space}
	for _, t := range ls.Terminals {
		if spaceOf(t.Where) == space {
			p.Panes = append(p.Panes, t.Address)
		}
	}
	return p
}

func spaceOf(where string) string {
	if i := strings.Index(where, " / "); i >= 0 {
		return where[:i]
	}
	return where
}

func locateHerdr(ctx context.Context, bin, ws string, run Runner) Place {
	p := Place{Host: "herdr"}
	out, err := run(ctx, bin, "workspace", "list")
	if err != nil {
		return p
	}
	var wl struct {
		Result struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &wl) != nil {
		return p
	}
	for _, w := range wl.Result.Workspaces {
		if w.ID == ws {
			p.Project = w.Label
		}
	}
	if out, err = run(ctx, bin, "pane", "list"); err != nil {
		return p
	}
	var pl struct {
		Result struct {
			Panes []struct {
				ID        string `json:"pane_id"`
				Workspace string `json:"workspace_id"`
			} `json:"panes"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &pl) != nil {
		return p
	}
	for _, x := range pl.Result.Panes {
		if x.Workspace == ws {
			p.Panes = append(p.Panes, x.ID)
		}
	}
	return p
}

// projectFor picks the Work project for a workspace. Sessions can be moved to
// another project in Amythest, so the projects of the workspace's own live
// sessions decide; the workspace name is the fallback, matched without regard
// to case against the projects that exist.
func projectFor(p Place, o *apiclient.WorkSnapshot) string {
	if o != nil && len(p.Panes) > 0 {
		in := map[string]bool{}
		for _, id := range p.Panes {
			in[id] = true
		}
		votes := map[string]int{}
		for _, s := range o.Slots {
			if in[s.Pane] && s.Project != "" {
				votes[s.Project]++
			}
		}
		// A pane id is only unique per machine, so a single stray match on
		// another machine must not outvote the workspace's own name.
		names := make([]string, 0, len(votes))
		for n := range votes {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool {
			if votes[names[i]] != votes[names[j]] {
				return votes[names[i]] > votes[names[j]]
			}
			return names[i] < names[j]
		})
		for _, n := range names {
			if strings.EqualFold(n, p.Project) {
				return n
			}
		}
		if len(names) > 0 && votes[names[0]] > 1 {
			return names[0]
		}
	}
	if o != nil {
		for _, n := range o.Projects {
			if strings.EqualFold(n, p.Project) {
				return n
			}
		}
	}
	return p.Project
}
