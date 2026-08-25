package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
)

// browseRow is one visible line of the notes browser: a folder, a note, or a
// virtual folder (Recent / Untagged / Tags / a tag).
type browseRow struct {
	depth  int
	label  string
	folder string // expand key for folder-ish rows ("" for notes)
	slug   string // note identity for note rows ("" otherwise)
	count  int
	kind   string // "folder" | "note" | "virtual" | "tag"
}

type browseState struct {
	notes     []apiclient.NoteEntry
	index     map[string]apiclient.ContentEntry
	expanded  map[string]bool
	rows      []browseRow
	cursor    int
	offset    int
	sortByTitle bool
	filter    string
	loaded    bool
}

const (
	browseRecent   = "@recent"
	browseUntagged = "@untagged"
	browseTags     = "@tags"
)

// rebuild flattens the tree according to expansion, sort, and filter state.
func (b *browseState) rebuild(now time.Time) {
	if b.expanded == nil {
		b.expanded = map[string]bool{}
	}
	if b.filter != "" {
		b.rows = filterBrowseRows(b.notes, b.index, b.filter, b.sortByTitle)
	} else {
		b.rows = buildBrowseRows(b.notes, b.index, b.expanded, b.sortByTitle, now)
	}
	if b.cursor >= len(b.rows) {
		b.cursor = max(0, len(b.rows)-1)
	}
}

// buildBrowseRows produces the virtual folders then the folder tree.
func buildBrowseRows(notes []apiclient.NoteEntry, index map[string]apiclient.ContentEntry, expanded map[string]bool, sortByTitle bool, now time.Time) []browseRow {
	var rows []browseRow

	// Recent: modified within 7 days, newest first, capped.
	cutoff := now.AddDate(0, 0, -7).Unix()
	var recent []apiclient.NoteEntry
	for _, n := range notes {
		if n.MTime >= cutoff {
			recent = append(recent, n)
		}
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].MTime > recent[j].MTime })
	if len(recent) > 20 {
		recent = recent[:20]
	}
	rows = append(rows, browseRow{label: "Recent", folder: browseRecent, count: len(recent), kind: "virtual"})
	if expanded[browseRecent] {
		for _, n := range recent {
			rows = append(rows, browseRow{depth: 1, label: n.Title, slug: n.Slug, kind: "note"})
		}
	}

	// Untagged.
	var untagged []apiclient.NoteEntry
	for _, n := range notes {
		if len(index[n.Slug].Tags) == 0 {
			untagged = append(untagged, n)
		}
	}
	sortNotes(untagged, sortByTitle)
	rows = append(rows, browseRow{label: "Untagged", folder: browseUntagged, count: len(untagged), kind: "virtual"})
	if expanded[browseUntagged] {
		for _, n := range untagged {
			rows = append(rows, browseRow{depth: 1, label: n.Title, slug: n.Slug, kind: "note"})
		}
	}

	// Tags → one child folder per tag, by descending use.
	tagCounts := map[string]int{}
	for _, entry := range index {
		for _, tag := range entry.Tags {
			tagCounts[tag]++
		}
	}
	tags := make([]string, 0, len(tagCounts))
	for tag := range tagCounts {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		if tagCounts[tags[i]] != tagCounts[tags[j]] {
			return tagCounts[tags[i]] > tagCounts[tags[j]]
		}
		return tags[i] < tags[j]
	})
	rows = append(rows, browseRow{label: "Tags", folder: browseTags, count: len(tags), kind: "virtual"})
	if expanded[browseTags] {
		for _, tag := range tags {
			key := "@tag:" + tag
			rows = append(rows, browseRow{depth: 1, label: "#" + tag, folder: key, count: tagCounts[tag], kind: "tag"})
			if expanded[key] {
				var tagged []apiclient.NoteEntry
				for _, n := range notes {
					for _, nt := range index[n.Slug].Tags {
						if nt == tag {
							tagged = append(tagged, n)
							break
						}
					}
				}
				sortNotes(tagged, sortByTitle)
				for _, n := range tagged {
					rows = append(rows, browseRow{depth: 2, label: n.Title, slug: n.Slug, kind: "note"})
				}
			}
		}
	}

	// Folder tree from paths.
	rows = append(rows, folderRows(notes, "", 0, expanded, sortByTitle)...)
	return rows
}

// folderRows renders one directory level: subfolders first, then notes.
func folderRows(notes []apiclient.NoteEntry, prefix string, depth int, expanded map[string]bool, sortByTitle bool) []browseRow {
	type folderInfo struct{ count int }
	folders := map[string]*folderInfo{}
	var direct []apiclient.NoteEntry
	for _, n := range notes {
		if prefix != "" && !strings.HasPrefix(n.Path, prefix+"/") {
			continue
		}
		rest := n.Path
		if prefix != "" {
			rest = strings.TrimPrefix(n.Path, prefix+"/")
		}
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			name := rest[:idx]
			if folders[name] == nil {
				folders[name] = &folderInfo{}
			}
			folders[name].count++
		} else {
			direct = append(direct, n)
		}
	}
	names := make([]string, 0, len(folders))
	for name := range folders {
		names = append(names, name)
	}
	sort.Strings(names)

	var rows []browseRow
	for _, name := range names {
		full := name
		if prefix != "" {
			full = prefix + "/" + name
		}
		rows = append(rows, browseRow{depth: depth, label: name + "/", folder: full, count: folders[name].count, kind: "folder"})
		if expanded[full] {
			rows = append(rows, folderRows(notes, full, depth+1, expanded, sortByTitle)...)
		}
	}
	sortNotes(direct, sortByTitle)
	for _, n := range direct {
		rows = append(rows, browseRow{depth: depth, label: n.Title, slug: n.Slug, kind: "note"})
	}
	return rows
}

func sortNotes(notes []apiclient.NoteEntry, byTitle bool) {
	if byTitle {
		sort.Slice(notes, func(i, j int) bool {
			return strings.ToLower(notes[i].Title) < strings.ToLower(notes[j].Title)
		})
		return
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].MTime > notes[j].MTime })
}

// filterBrowseRows applies the clin-style DSL: space-separated terms ANDed;
// `t:tag` matches tags, `f:folder` matches a path prefix segment, everything
// else is a case-insensitive title substring.
func filterBrowseRows(notes []apiclient.NoteEntry, index map[string]apiclient.ContentEntry, filter string, sortByTitle bool) []browseRow {
	terms := strings.Fields(strings.ToLower(filter))
	var matched []apiclient.NoteEntry
	for _, n := range notes {
		if matchesFilter(n, index[n.Slug], terms) {
			matched = append(matched, n)
		}
	}
	sortNotes(matched, sortByTitle)
	rows := make([]browseRow, 0, len(matched))
	for _, n := range matched {
		rows = append(rows, browseRow{label: n.Title, slug: n.Slug, kind: "note"})
	}
	return rows
}

func matchesFilter(n apiclient.NoteEntry, entry apiclient.ContentEntry, terms []string) bool {
	for _, term := range terms {
		switch {
		case strings.HasPrefix(term, "t:"):
			want := strings.TrimPrefix(term, "t:")
			found := false
			for _, tag := range entry.Tags {
				if strings.Contains(strings.ToLower(tag), want) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case strings.HasPrefix(term, "f:"):
			want := strings.TrimPrefix(term, "f:")
			if !strings.Contains(strings.ToLower(n.Path), want) {
				return false
			}
		default:
			if !strings.Contains(strings.ToLower(n.Title), term) {
				return false
			}
		}
	}
	return true
}
