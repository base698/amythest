// Package herdr shells out to the herdr CLI (terminal workspace manager) to
// list running coding agents and hand one of them a note as context. herdr
// speaks JSON on stdout over its socket API; absent or stopped herdr just
// surfaces as an error the UI can flash.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Agent struct {
	PaneID   string `json:"pane_id"`
	Agent    string `json:"agent"`
	Status   string `json:"agent_status"`
	Cwd      string `json:"cwd"`
	Title    string `json:"terminal_title_stripped"`
	Focused  bool   `json:"focused"`
	Terminal string `json:"terminal_id"`
}

type listResponse struct {
	Result struct {
		Agents []Agent `json:"agents"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// List returns the running agents herdr knows about.
func List(ctx context.Context) ([]Agent, error) {
	out, err := run(ctx, "agent", "list")
	if err != nil {
		return nil, err
	}
	var resp listResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse herdr agent list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("herdr: %s", resp.Error.Message)
	}
	return resp.Result.Agents, nil
}

// Prompt submits text to the agent in the given pane.
func Prompt(ctx context.Context, target, text string) error {
	out, err := run(ctx, "agent", "prompt", target, text)
	if err != nil {
		return err
	}
	var resp listResponse
	if err := json.Unmarshal(out, &resp); err == nil && resp.Error != nil {
		return fmt.Errorf("herdr: %s", resp.Error.Message)
	}
	return nil
}

func run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("herdr %s: %s", strings.Join(args[:2], " "), detail)
	}
	return stdout.Bytes(), nil
}
