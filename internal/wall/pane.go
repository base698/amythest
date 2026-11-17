package wall

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"
)

// A wall opened by a key owns its pane in the Herdr App: the same key closes
// it again, and quitting the wall closes the pane instead of leaving a shell
// behind. The App runs every program inside the user's shell, so "close when
// the program ends" has to be asked for.

// AppPane finds this terminal's pane and the panes of other walls in the same
// tab. self is HC_TERMINAL_ID.
func AppPane(self string, run Runner) (own string, others []string) {
	if self == "" {
		return "", nil
	}
	if run == nil {
		run = execRunner
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := run(ctx, "herdr-app", "ls")
	if err != nil {
		return "", nil
	}
	var ls struct {
		Spaces []struct {
			Tabs []struct {
				Terminals []struct {
					Terminal string `json:"terminal"`
					Pane     string `json:"pane"`
				} `json:"terminals"`
			} `json:"tabs"`
		} `json:"spaces"`
	}
	if json.Unmarshal(out, &ls) != nil {
		return "", nil
	}
	for _, s := range ls.Spaces {
		for _, t := range s.Tabs {
			mine := false
			for _, x := range t.Terminals {
				if x.Terminal == self {
					mine, own = true, x.Pane
				}
			}
			if !mine {
				continue
			}
			for _, x := range t.Terminals {
				if x.Terminal != self && runsWall(ctx, x.Terminal, run) {
					others = append(others, x.Pane)
				}
			}
			return own, others
		}
	}
	return "", nil
}

// runsWall reports whether a terminal's program is `amy wall`.
func runsWall(ctx context.Context, terminal string, run Runner) bool {
	out, err := run(ctx, "hc", "get", terminal, "--json")
	if err != nil {
		return false
	}
	var t struct {
		Foreground *struct {
			Argv []string `json:"argv"`
		} `json:"foreground"`
	}
	if json.Unmarshal(out, &t) != nil || t.Foreground == nil {
		return false
	}
	return isWall(t.Foreground.Argv)
}

func isWall(argv []string) bool {
	for i, a := range argv {
		if filepath.Base(a) == "amy" && i+1 < len(argv) && argv[i+1] == "wall" {
			return true
		}
	}
	return false
}

// ClosePane closes a Herdr App pane.
func ClosePane(pane string, run Runner) error {
	if run == nil {
		run = execRunner
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := run(ctx, "herdr-app", "do", "pane.close", "--pane", pane)
	return err
}
