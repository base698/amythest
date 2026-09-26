package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// `amy herdr find-pane --label amy` prints "<tab_id> <pane_id>" for the
// first open pane carrying that label, or exits 1 when none is open.
//
// It exists so the plugin's open-board action can focus an already-running
// board instead of splitting a second one — the difference between feeling
// native and collecting stray panes. Shelling to jq would be lighter, but
// jq is not a dependency we can assume on every machine.

func runHerdr(args []string) {
	if len(args) == 0 || args[0] != "find-pane" {
		fmt.Fprintln(os.Stderr, "usage: amy herdr find-pane --label <label>")
		os.Exit(2)
	}
	label := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--label":
			if i+1 < len(args) {
				label = args[i+1]
				i++
			}
		default:
			if value, ok := strings.CutPrefix(args[i], "--label="); ok {
				label = value
			}
		}
	}
	if label == "" {
		fmt.Fprintln(os.Stderr, "usage: amy herdr find-pane --label <label>")
		os.Exit(2)
	}
	tab, pane, err := findPane(label)
	if err != nil {
		fmt.Fprintln(os.Stderr, "find-pane:", err)
		os.Exit(2)
	}
	if pane == "" {
		os.Exit(1) // not open; the caller opens one
	}
	fmt.Printf("%s %s\n", tab, pane)
}

type paneListResponse struct {
	Result struct {
		Panes []struct {
			Label       string `json:"label"`
			PaneID      string `json:"pane_id"`
			TabID       string `json:"tab_id"`
			WorkspaceID string `json:"workspace_id"`
		} `json:"panes"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func findPane(label string) (tabID, paneID string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Inside a plugin command herdr exports its own binary path; fall back
	// to PATH so the helper also works from an ordinary shell.
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		binary = "herdr"
	}
	cmd := exec.CommandContext(ctx, binary, "pane", "list")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", "", fmt.Errorf("herdr pane list: %s", detail)
	}
	var resp paneListResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return "", "", fmt.Errorf("parse herdr pane list: %w", err)
	}
	if resp.Error != nil {
		return "", "", fmt.Errorf("herdr: %s", resp.Error.Message)
	}
	// Prefer a pane in the workspace we were invoked from, so a board open
	// in another workspace does not steal the action.
	workspace := os.Getenv("HERDR_WORKSPACE_ID")
	var fallbackTab, fallbackPane string
	for _, pane := range resp.Result.Panes {
		if !strings.EqualFold(strings.TrimSpace(pane.Label), label) {
			continue
		}
		if workspace != "" && pane.WorkspaceID == workspace {
			return pane.TabID, pane.PaneID, nil
		}
		if fallbackPane == "" {
			fallbackTab, fallbackPane = pane.TabID, pane.PaneID
		}
	}
	return fallbackTab, fallbackPane, nil
}
