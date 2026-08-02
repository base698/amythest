package server

import (
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
)

func TestTasksToolbarActiveChipsToggleOff(t *testing.T) {
	s := &Server{cfg: config.Config{BaseURL: "/"}}
	for _, tc := range []struct {
		name                   string
		status, sortKey, group string
		wantChip               string // rendered chip anchor that must appear
	}{
		{
			name:   "active group chip clears group",
			status: "open", group: "folder",
			wantChip: `<a class="chip active" href="/tasks?status=open">folder</a>`,
		},
		{
			name:   "inactive group chip sets group",
			status: "open", group: "folder",
			wantChip: `<a class="chip" href="/tasks?status=open&amp;group=priority">priority</a>`,
		},
		{
			name:    "active sort chip clears sort",
			status:  "open",
			sortKey: "due",
			wantChip: `<a class="chip active" href="/tasks?status=open">due</a>`,
		},
		{
			name:   "active status chip clears status",
			status: "open",
			wantChip: `<a class="chip active" href="/tasks">Open</a>`,
		},
		{
			name:   "status toggle keeps sort and group",
			status: "open", sortKey: "due", group: "folder",
			wantChip: `<a class="chip active" href="/tasks?sort=due&amp;group=folder">Open</a>`,
		},
		{
			name:     "dashboard is active only when everything is clear",
			wantChip: `<a class="chip active" href="/tasks">Dashboard</a>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := s.tasksToolbar(tc.status, tc.sortKey, tc.group)
			if !strings.Contains(html, tc.wantChip) {
				t.Fatalf("toolbar missing %q in:\n%s", tc.wantChip, html)
			}
		})
	}
}
