package wall

import (
	"path"
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
)

// Item is one entry of the feed.
type Item struct {
	Art    apiclient.WorkArtifact
	Kind   string // commit, pr, image, video, file or canvas
	Title  string
	Tab    string // the tab (or session) that produced it
	Agent  string
	When   time.Time
	Canvas *apiclient.CanvasMeta
}

func withCanvases(items []Item, canvases []apiclient.CanvasMeta) []Item {
	for _, c := range canvases {
		items = append(items, Item{
			Art:  apiclient.WorkArtifact{ID: "canvas:" + c.ID, Kind: "canvas", Ref: c.ID, Title: c.Title},
			Kind: "canvas", Title: c.Title, When: c.Updated, Canvas: &c,
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].When.After(items[j].When) })
	return items
}

// buildFeed turns a project snapshot into feed items, newest first. An
// artifact linked to several runs appears once.
func buildFeed(s *apiclient.WorkSnapshot) []Item {
	session := map[string]apiclient.WorkSession{}
	for _, x := range s.Sessions {
		session[x.ID] = x
	}
	run := map[string]apiclient.WorkRun{}
	for _, r := range s.Runs {
		run[r.ID] = r
	}
	seen := map[string]bool{}
	items := []Item{}
	for _, a := range s.Artifacts {
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		it := Item{Art: a, Kind: kindOf(a), Title: titleOf(a)}
		it.When, _ = time.Parse(time.RFC3339Nano, a.Created)
		if r, ok := run[a.RunID]; ok {
			if x, ok := session[r.Session]; ok {
				it.Tab, it.Agent = x.Title, x.Agent
			}
		}
		items = append(items, it)
	}
	return items
}

func kindOf(a apiclient.WorkArtifact) string {
	switch a.Kind {
	case "commit", "pr", "image", "video":
		return a.Kind
	}
	switch strings.ToLower(path.Ext(refName(a))) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return "image"
	case ".mp4", ".mov", ".webm", ".mkv":
		return "video"
	}
	return "file"
}

// refName is the file name an artifact goes by.
func refName(a apiclient.WorkArtifact) string {
	for _, s := range []string{a.RemotePath, a.Ref, a.Title} {
		if s != "" {
			return path.Base(strings.ReplaceAll(s, "\\", "/"))
		}
	}
	return ""
}

func titleOf(a apiclient.WorkArtifact) string {
	switch a.Kind {
	case "commit":
		if a.Title != "" {
			return a.Title
		}
		return "commit " + shortRef(a.Ref)
	case "pr":
		if a.Title != "" {
			return a.Title
		}
		return a.Ref
	}
	if a.Title != "" && a.Title != a.Ref {
		return a.Title
	}
	return refName(a)
}

func shortRef(ref string) string {
	if len(ref) > 8 {
		return ref[:8]
	}
	return ref
}

// matches reports whether an item passes an all/media/code/canvas filter.
func (it Item) matches(filter string) bool {
	switch filter {
	case "media":
		return it.Kind == "image" || it.Kind == "video"
	case "code":
		return it.Kind == "commit" || it.Kind == "pr"
	case "canvas":
		return it.Kind == "canvas"
	}
	return true
}

// webURL is where an item opens in a browser: the commit on GitHub when the
// repository is there, otherwise the uploaded file on the Amythest server.
func (it Item) webURL(endpoint string) string {
	a := it.Art
	if it.Canvas != nil {
		return strings.TrimRight(endpoint, "/") + "/canvas/" + it.Canvas.ID
	}
	if a.Kind == "pr" && strings.HasPrefix(a.Ref, "http") {
		return a.Ref
	}
	if a.Kind == "commit" {
		if repo := githubRepo(a.Repo); repo != "" && a.Ref != "" {
			return "https://github.com/" + repo + "/commit/" + a.Ref
		}
	}
	if a.MediaHash != "" && a.Available {
		return strings.TrimRight(endpoint, "/") + "/api/work/media/" + a.MediaHash
	}
	return ""
}

// githubRepo extracts owner/name from the forms a repository is recorded in.
func githubRepo(repo string) string {
	r := strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:", "github.com/", "github.com:"} {
		if rest, ok := strings.CutPrefix(r, prefix); ok {
			if parts := strings.Split(rest, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				return rest
			}
			return ""
		}
	}
	return ""
}

// ago is a short relative time: "now", "5m", "3h", "2d", then a date.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + "h"
	case d < 14*24*time.Hour:
		return itoa(int(d/(24*time.Hour))) + "d"
	}
	return t.Local().Format("Jan 2")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
