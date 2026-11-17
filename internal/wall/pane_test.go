package wall

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestToggleOnlyMatchingOwnedPanesInCurrentTab(t *testing.T) {
	for _, command := range []string{"messages", "find", "wall"} {
		t.Run(command, func(t *testing.T) {
			var closed []string
			replies := map[string]string{
				"herdr-app ls":            `{"spaces":[{"tabs":[{"terminals":[{"terminal":"self","pane":"own"},{"terminal":"matching","pane":"old"},{"terminal":"duplicate","pane":"duplicate"},{"terminal":"other","pane":"other"},{"terminal":"shell","pane":"shell"}]},{"terminals":[{"terminal":"another-tab","pane":"elsewhere"}]}]}]}`,
				"hc get matching --json":  `{"foreground":{"argv":["/opt/bin/amy","` + command + `","-pane"]}}`,
				"hc get duplicate --json": `{"foreground":{"argv":["amy","` + command + `","-pane"]}}`,
				"hc get other --json":     `{"foreground":{"argv":["amy","capture"]}}`,
				"hc get shell --json":     `{"foreground":{"argv":["amy","` + command + `"]}}`,
			}
			run := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "herdr-app" && len(args) == 4 && args[0] == "do" && args[1] == "pane.close" {
					closed = append(closed, args[3])
					return nil, nil
				}
				key := name + " " + strings.Join(args, " ")
				if v, ok := replies[key]; ok {
					return []byte(v), nil
				}
				t.Fatalf("unexpected lookup outside this tab: %s", key)
				return nil, nil
			}
			own, toggled, err := ToggleAppPane("self", command, run)
			if err != nil || !toggled || own != "own" || !reflect.DeepEqual(closed, []string{"old", "duplicate", "own"}) {
				t.Fatalf("toggle: %q %v %v, closed %v", own, toggled, err, closed)
			}
		})
	}
}

func TestToggleWaitsForPlacementAndLeavesFirstPaneOpen(t *testing.T) {
	oldWait := placementWait
	placementWait = 0
	t.Cleanup(func() { placementWait = oldWait })
	reads := 0
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "herdr-app" || strings.Join(args, " ") != "ls" {
			t.Fatal("unexpected close or lookup")
		}
		reads++
		if reads == 1 {
			return []byte(`{"spaces":[]}`), nil
		}
		return []byte(`{"spaces":[{"tabs":[{"terminals":[{"terminal":"self","pane":"own"}]}]}]}`), nil
	}
	own, closed, err := ToggleAppPane("self", "find", run)
	if err != nil || closed || own != "own" || reads != 2 {
		t.Fatalf("%q %v %v %d", own, closed, err, reads)
	}
	own, closed, err = ToggleAppPane("", "messages", run)
	if err != nil || closed || own != "" || reads != 2 {
		t.Fatal("standalone invocation used the app")
	}
}

func TestToggleDoesNotCloseSelfAfterExistingPaneFails(t *testing.T) {
	failure := errors.New("pane unavailable")
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name + " " + strings.Join(args, " ") {
		case "herdr-app ls":
			return []byte(`{"spaces":[{"tabs":[{"terminals":[{"terminal":"self","pane":"own"},{"terminal":"old","pane":"previous"}]}]}]}`), nil
		case "hc get old --json":
			return []byte(`{"foreground":{"argv":["amy","messages","-pane"]}}`), nil
		case "herdr-app do pane.close --pane previous":
			return nil, failure
		default:
			t.Fatal("closed self after failure")
			return nil, nil
		}
	}
	_, closed, err := ToggleAppPane("self", "messages", run)
	if closed || !errors.Is(err, failure) {
		t.Fatalf("%v %v", closed, err)
	}
}

func TestPaneCommandDistinguishesToolsAndGateway(t *testing.T) {
	for _, tc := range []struct {
		argv    []string
		command string
		want    bool
	}{
		{[]string{"amy", "messages", "-pane"}, "messages", true},
		{[]string{"amy", "messages", "focus", "-pane"}, "messages", true},
		{[]string{"amy", "messages", "serve", "-pane"}, "messages", false},
		{[]string{"amy", "messages", "-pane=false"}, "messages", false},
		{[]string{"amy", "find", "-pane"}, "messages", false},
		{[]string{"amy", "wall", "-pane"}, "find", false},
		{[]string{"amy", "find", "--pane=true"}, "find", true},
		{[]string{"amy", "messages"}, "messages", false},
		{[]string{"echo", "amy", "find", "-pane"}, "find", false},
		{[]string{"amy", "find", "query", "-pane"}, "find", false},
		{[]string{"amy", "find", "--", "-pane"}, "find", false},
		{[]string{"amy", "find", "-config", "-pane"}, "find", false},
		{[]string{"amy", "find", "-config", "settings.yaml", "-pane", "query"}, "find", true},
	} {
		if got := isPaneCommand(tc.argv, tc.command); got != tc.want {
			t.Errorf("%v for %s: %v", tc.argv, tc.command, got)
		}
	}
}
