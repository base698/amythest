package tasks

import (
	"regexp"
	"strings"
)

var (
	taskLineRe    = regexp.MustCompile(`^\s*[-*+]\s+\[(.)\]\s+(.*)$`)
	dateRe        = regexp.MustCompile(`^\s*(\d{4}-\d{2}-\d{2})`)
	tagRe         = regexp.MustCompile(`#([\p{L}\p{N}_/-]+)`)
	inlineFieldRe = regexp.MustCompile(`\[([^\[\]():]{1,64})::\s*([^\]]*)\]`)
	fenceRe       = regexp.MustCompile("^\\s*(```|~~~)")
)

// emoji metadata markers, in the order Obsidian Tasks renders them.
var emojiMarkers = []struct {
	marker string
	field  string
}{
	{"📅", "due"},
	{"⏳", "scheduled"},
	{"🛫", "start"},
	{"🔁", "recurrence"},
	{"✅", "done"},
	{"❌", "cancelled"},
	{"🔺", "prio0"},
	{"⏫", "prio1"},
	{"🔼", "prio2"},
	{"🔽", "prio4"},
	{"⏬", "prio5"},
	{"➕", "created"},
}

// ParseFile extracts tasks and inline fields from a note body. Fenced code
// blocks are skipped so examples in documentation don't index as tasks.
func ParseFile(slug, path string, body []byte) ([]Task, []InlineField) {
	var out []Task
	var fields []InlineField
	inFence := false
	lineNo := 0
	for rawLine := range strings.Lines(string(body)) {
		lineNo++
		// strings.Lines keeps the newline, which defeats the $ anchor.
		line := strings.TrimRight(rawLine, "\r\n")
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range inlineFieldRe.FindAllStringSubmatch(line, -1) {
			fields = append(fields, InlineField{
				Line: lineNo, Key: strings.TrimSpace(m[1]), Value: strings.TrimSpace(m[2]),
			})
		}
		m := taskLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		t := parseTask(m[1][0], m[2])
		t.Slug = slug
		t.Path = path
		t.Line = lineNo
		out = append(out, t)
	}
	return out, fields
}

// ParseLine parses one raw checkbox line into a Task carrying the given
// vault coordinates. ok is false when the line is not a task checkbox.
func ParseLine(line, slug, path string, lineNo int, version string) (Task, bool) {
	m := taskLineRe.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
	if m == nil {
		return Task{}, false
	}
	t := parseTask(m[1][0], m[2])
	t.Slug, t.Path, t.Line, t.Version = slug, path, lineNo, version
	return t, true
}

func parseTask(statusChar byte, rest string) Task {
	t := Task{Priority: 3}
	switch statusChar {
	case ' ':
		t.Status = StatusOpen
	case 'x', 'X':
		t.Status = StatusDone
	case '-':
		t.Status = StatusCancelled
	default:
		t.Status = StatusOther
	}

	// Split the text at the first metadata emoji; scan markers from there.
	text := rest
	cut := len(rest)
	for _, em := range emojiMarkers {
		if i := strings.Index(rest, em.marker); i >= 0 && i < cut {
			cut = i
		}
	}
	text, meta := rest[:cut], rest[cut:]

	for _, em := range emojiMarkers {
		i := strings.Index(meta, em.marker)
		if i < 0 {
			continue
		}
		after := meta[i+len(em.marker):]
		switch em.field {
		case "due":
			t.Due = firstDate(after)
		case "scheduled":
			t.Scheduled = firstDate(after)
		case "start":
			t.Start = firstDate(after)
		case "done":
			t.DoneDate = firstDate(after)
		case "cancelled":
			if t.Status == StatusDone { // ❌ with [x] is unusual; keep done date
				break
			}
			t.DoneDate = firstDate(after)
		case "recurrence":
			t.Recurrence = untilNextMarker(after)
		case "prio0":
			t.Priority = 0
		case "prio1":
			t.Priority = 1
		case "prio2":
			t.Priority = 2
		case "prio4":
			t.Priority = 4
		case "prio5":
			t.Priority = 5
		}
	}

	// Tags can appear before or after the metadata emojis; scan everything.
	for _, m := range tagRe.FindAllStringSubmatch(rest, -1) {
		t.Tags = append(t.Tags, strings.ToLower(m[1]))
	}
	t.Text = strings.TrimSpace(text)
	return t
}

func firstDate(s string) string {
	if m := dateRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// untilNextMarker returns text up to the next emoji marker.
func untilNextMarker(s string) string {
	end := len(s)
	for _, em := range emojiMarkers {
		if i := strings.Index(s, em.marker); i >= 0 && i < end {
			end = i
		}
	}
	return strings.TrimSpace(s[:end])
}
