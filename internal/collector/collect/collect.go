// Package collect finds what an agent run produced: commits, the PR for its
// branch, new media files in its repo, and PR URLs / media paths mentioned in
// its Claude Code transcript.
package collect

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Owner says which work the artifact belongs to.
type Owner struct {
	Machine   string `json:"machine"`
	Project   string `json:"project"`   // herdr workspace label
	WorkItem  string `json:"work_item"` // herdr tab label
	Pane      string `json:"pane"`
	Agent     string `json:"agent,omitempty"`
	Session   string `json:"session,omitempty"`
	Attribute string `json:"attribution"` // exact | branch | path | transcript
}

type Artifact struct {
	Key      string    `json:"key"`
	Kind     string    `json:"kind"` // commit | pr | image | video | file
	Ref      string    `json:"ref"`  // sha, URL, or absolute path
	Title    string    `json:"title,omitempty"`
	Repo     string    `json:"repo,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Found    string    `json:"found_by"`           // git | gh | fswalk | transcript | hook
	Count    int       `json:"count,omitempty"`    // image sets: number of files
	Featured bool      `json:"featured,omitempty"` // the agent mentioned it in chat (or attached it)
	Time     time.Time `json:"time"`
	Owner    Owner     `json:"owner"`
}

// Run describes one agent run to collect for.
type Run struct {
	Owner Owner
	Dir   string    // pane cwd
	Since time.Time // when the run started working
}

var (
	mediaExt = map[string]string{
		".gif": "image", ".png": "image", ".jpg": "image", ".jpeg": "image", ".webp": "image", ".svg": "image",
		".mp4": "video", ".mov": "video", ".webm": "video", ".mkv": "video",
	}
	skipDirs = map[string]bool{
		".git": true, "node_modules": true, "target": true, ".venv": true, "venv": true,
		"__pycache__": true, ".next": true, "dist": true, "build": true, ".cache": true,
		"install": true, "log": true, ".pytest_cache": true, ".claude": true,
	}
	prURL     = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)
	mediaPath = regexp.MustCompile("([\\w./~-]*/[\\w.~-]+\\.(?i:gif|png|jpe?g|webp|mp4|mov|webm|mkv))\\b")
)

// RepoInfo resolves a directory to its git toplevel and branch.
func RepoInfo(dir string) (top, branch string) {
	out, err := boundedOutput("", "git", "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ""
	}
	top = strings.TrimSpace(string(out))
	out, _ = boundedOutput("", "git", "-C", dir, "branch", "--show-current")
	return top, strings.TrimSpace(string(out))
}

// Collect gathers artifacts for a run. Errors from individual detectors are
// ignored: each detector is best-effort.
func Collect(r Run) []Artifact {
	var arts []Artifact
	top, branch := RepoInfo(r.Dir)
	// A repo with no commits (e.g. a parent folder that is only a git root) is
	// not a project; walking it would scan every project beneath it.
	_, headErr := boundedOutput("", "git", "-C", top, "rev-parse", "--verify", "-q", "HEAD")
	if top != "" && headErr != nil {
		top = ""
	}
	if top != "" {
		arts = append(arts, commits(r, top, branch)...)
		arts = append(arts, pr(r, top, branch)...)
		arts = append(arts, media(r, top, branch)...)
	}
	if r.Owner.Session != "" {
		arts = append(arts, transcript(r)...)
	}
	return merge(arts)
}

// merge dedupes across detectors (a file found by the walk and mentioned in the
// transcript is one artifact, marked featured), then folds folders full of
// still images that nobody mentioned into one "image set" per folder.
func merge(arts []Artifact) []Artifact {
	byKey := map[string]int{}
	var out []Artifact
	for _, a := range arts {
		if a.Found == "transcript" || a.Found == "hook" {
			a.Featured = true
		}
		if i, ok := byKey[a.Key]; ok {
			if a.Featured {
				out[i].Featured = true
				out[i].Found += "+" + a.Found
			}
			if out[i].Title == "" {
				out[i].Title = a.Title
			}
			continue
		}
		byKey[a.Key] = len(out)
		out = append(out, a)
	}
	const setMin = 5
	stills := map[string][]int{}
	for i, a := range out {
		if a.Kind == "image" && !a.Featured && strings.ToLower(filepath.Ext(a.Ref)) != ".gif" {
			d := filepath.Dir(a.Ref)
			stills[d] = append(stills[d], i)
		}
	}
	drop := map[int]bool{}
	var sets []Artifact
	for d, idx := range stills {
		if len(idx) < setMin {
			continue
		}
		first, last := out[idx[0]], out[idx[0]]
		for _, i := range idx {
			drop[i] = true
			if out[i].Time.After(last.Time) {
				last = out[i]
			}
		}
		set := first
		set.Kind, set.Ref, set.Title, set.Count, set.Time = "imageset", d, filepath.Base(d), len(idx), last.Time
		set.Key = "set:" + d + "@" + last.Time.UTC().Format(time.RFC3339)
		sets = append(sets, set)
	}
	var final []Artifact
	for i, a := range out {
		if !drop[i] {
			final = append(final, a)
		}
	}
	return append(final, sets...)
}

func commits(r Run, top, branch string) []Artifact {
	out, err := boundedOutput("", "git", "-C", top, "log", "--all", "-n", "1000",
		"--since="+r.Since.Format(time.RFC3339), "--format=%H%x1f%s%x1f%cI")
	if err != nil {
		return nil
	}
	var arts []Artifact
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 3 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, f[2])
		o := r.Owner
		o.Attribute = "branch"
		arts = append(arts, Artifact{Key: "commit:" + f[0], Kind: "commit", Ref: f[0], Title: f[1],
			Repo: top, Branch: branch, Found: "git", Time: t, Owner: o})
	}
	return arts
}

func pr(r Run, top, branch string) []Artifact {
	if branch == "" || branch == "main" || branch == "master" {
		return nil
	}
	out, err := boundedOutput(top, "gh", "pr", "view", branch, "--json", "number,url,title,state,updatedAt")
	if err != nil {
		return nil
	}
	var p struct {
		URL       string    `json:"url"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	if json.Unmarshal(out, &p) != nil || p.URL == "" {
		return nil
	}
	o := r.Owner
	o.Attribute = "branch"
	return []Artifact{{Key: "pr:" + p.URL, Kind: "pr", Ref: p.URL, Title: p.Title + " [" + p.State + "]",
		Repo: top, Branch: branch, Found: "gh", Time: p.UpdatedAt, Owner: o}}
}

// media walks the repo (including git-ignored output dirs) for media files
// modified since the run started. FSEvents replaces this in phase 2.
func media(r Run, top, branch string) []Artifact {
	var arts []Artifact
	visited := 0
	filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if visited++; visited > 200000 {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != top && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".venv") || d.Name() == "site-packages") {
				return filepath.SkipDir
			}
			return nil
		}
		kind, ok := mediaExt[strings.ToLower(filepath.Ext(p))]
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(r.Since) {
			return nil
		}
		o := r.Owner
		o.Attribute = "path"
		arts = append(arts, Artifact{Key: "file:" + p + "@" + info.ModTime().UTC().Format(time.RFC3339),
			Kind: kind, Ref: p, Title: filepath.Base(p), Repo: top, Branch: branch, Found: "fswalk",
			Time: info.ModTime(), Owner: o})
		return nil
	})
	return arts
}

// TranscriptPath finds a Claude Code transcript by session id.
func TranscriptPath(session string) string {
	home, _ := os.UserHomeDir()
	m, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", session+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

func transcript(r Run) []Artifact {
	path := TranscriptPath(r.Owner.Session)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	seen := map[string]bool{}
	var arts []Artifact
	for sc.Scan() {
		line := sc.Bytes()
		var ev struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.Timestamp.IsZero() || ev.Timestamp.Before(r.Since) {
			continue
		}
		o := r.Owner
		o.Attribute = "transcript"
		// PR URLs count from anywhere (gh pr create output lands in tool results).
		for _, u := range prURL.FindAllString(string(line), -1) {
			if !seen[u] {
				seen[u] = true
				arts = append(arts, Artifact{Key: "pr:" + u, Kind: "pr", Ref: u, Found: "transcript", Time: ev.Timestamp, Owner: o})
			}
		}
		// Media counts only when the agent names it to the user in its own reply
		// text; files it merely read or wrote with tools are not "featured".
		if ev.Type != "assistant" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "text" {
				continue
			}
			for _, m := range mediaPath.FindAllStringSubmatch(b.Text, -1) {
				p := m[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(r.Dir, p)
				}
				if seen[p] {
					continue
				}
				seen[p] = true
				info, err := os.Stat(p)
				if err != nil {
					continue // named but not on disk (or on another machine)
				}
				kind := mediaExt[strings.ToLower(filepath.Ext(p))]
				arts = append(arts, Artifact{Key: "file:" + p + "@" + info.ModTime().UTC().Format(time.RFC3339),
					Kind: kind, Ref: p, Title: filepath.Base(p), Found: "transcript", Time: ev.Timestamp, Owner: o})
			}
		}
	}
	return arts
}

func boundedOutput(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

// Repository identities are independent of the local checkout path when origin
// is a recognized SSH/HTTPS remote. Unknown/local remotes stay collector-local.
func CanonicalRepo(dir string) string {
	out, e := boundedOutput("", "git", "-C", dir, "remote", "get-url", "origin")
	if e != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
		s = strings.Replace(s, ":", "/", 1)
	} else if strings.HasPrefix(s, "https://") {
		s = strings.TrimPrefix(s, "https://")
	} else if strings.HasPrefix(s, "ssh://git@") {
		s = strings.TrimPrefix(s, "ssh://git@")
	} else {
		return ""
	}
	if strings.Contains(s, "@") || strings.Contains(s, "?") || strings.Contains(s, "#") {
		return ""
	}
	return strings.TrimSuffix(s, ".git")
}
