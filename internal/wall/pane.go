package wall

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A wall opened by a key owns its pane in the Herdr App: the same key closes
// it again, and quitting the wall closes the pane instead of leaving a shell
// behind. The App runs every program inside the user's shell, so "close when
// the program ends" has to be asked for.

// AppPane finds this terminal's pane and other panes owned by the same amy
// command in this tab. The default command is wall. self is HC_TERMINAL_ID.
func AppPane(self string, run Runner, commands ...string) (own string, others []string) {
	if self == "" {
		return "", nil
	}
	command := "wall"
	if len(commands) > 0 {
		command = commands[0]
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
				if x.Terminal != self && runsPaneCommand(ctx, x.Terminal, command, run) {
					others = append(others, x.Pane)
				}
			}
			return own, others
		}
	}
	return "", nil
}

func runsPaneCommand(ctx context.Context, terminal, command string, run Runner) bool {
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
	return isPaneCommand(t.Foreground.Argv, command)
}

func isPaneCommand(argv []string, command string) bool {
	if len(argv) < 2 || filepath.Base(argv[0]) != "amy" || argv[1] != command {
		return false
	}
	args := argv[2:]
	if command == "messages" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if args[0] != "focus" {
			return false
		}
		args = args[1:]
	}
	owned := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Go flags stop at positional arguments. A search query or a flag's
		// value containing "-pane" does not mean this program owns its pane.
		if !strings.HasPrefix(arg, "-") || arg == "--" {
			break
		}
		name, value, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "pane":
			owned = true
			if assigned {
				owned, _ = strconv.ParseBool(value)
			}
		case "no-screens", "list", "demo", "all", "trash", "stdin", "send", "files":
			// Other boolean flags do not consume the next argument.
		default:
			if !assigned {
				i++
			}
		}
	}
	return owned
}

// ToggleAppPane runs before network/configuration work. A repeated shortcut
// closes existing matching panes, then its own newly opened pane last. Closing
// our pane can terminate this process, so no required work may follow it.
func ToggleAppPane(self, command string, run Runner) (own string, closed bool, err error) {
	if self == "" {
		return "", false, nil
	}
	var others []string
	// Herdr creates the terminal before placing it in the tab's layout.
	for attempt := 0; ; attempt++ {
		own, others = AppPane(self, run, command)
		if own != "" || attempt >= placementTries {
			break
		}
		time.Sleep(placementWait)
	}
	if own == "" || len(others) == 0 {
		return own, false, nil
	}
	for _, pane := range others {
		if err = ClosePane(pane, run); err != nil {
			return own, false, fmt.Errorf("close existing %s pane: %w", command, err)
		}
	}
	err = ClosePane(own, run)
	return own, true, err
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
