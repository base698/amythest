package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
)

// A wall sends a byte snapshot of exactly the selected artifact. Source paths
// are labels only; they are never opened on this machine or sent as public URLs.
func shareWallItem(api *apiclient.Client, it wall.Item) tea.Cmd {
	return func() tea.Msg {
		client, e := loadMessageClient("", "", "owner", "")
		if e != nil {
			return wall.ShareDone{Err: e}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		name := filepath.Base(strings.ReplaceAll(it.Art.RemotePath, "\\", "/"))
		if name == "." || name == "" {
			name = filepath.Base(it.Art.Ref)
		}
		var data []byte
		if it.Art.MediaHash != "" && it.Art.Available {
			data, e = api.WorkMedia(ctx, it.Art.MediaHash, messages.MaxAttachment)
			if it.Kind == "commit" {
				ref := it.Art.Ref
				if len(ref) > 12 {
					ref = ref[:12]
				}
				name = "commit-" + ref + ".patch"
			}
		} else if it.Kind == "commit" || it.Kind == "pr" {
			name = "change-summary.txt"
			data = []byte(fmt.Sprintf("%s\n\nRepository: %s\nReference: %s\n\nNo captured diff is available for this item.\n", it.Title, it.Art.Repo, it.Art.Ref))
		} else {
			return wall.ShareDone{Err: fmt.Errorf("this item has no uploaded content to attach")}
		}
		if e != nil {
			return wall.ShareDone{Err: e}
		}
		if name == "." || name == "" {
			name = "wall-item"
		}
		a, e := client.Attach(ctx, name, data)
		if e != nil {
			return wall.ShareDone{Err: e}
		}
		binary, e := os.Executable()
		if e != nil {
			return wall.ShareDone{Err: e}
		}
		cmd := exec.Command(binary, "messages", "-attachment", a.ID)
		return tea.ExecProcess(cmd, func(e error) tea.Msg { return wall.ShareDone{Err: e} })()
	}
}
