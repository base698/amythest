package vault

import (
	"strings"
)

// Slugify converts a vault-relative markdown path to a URL slug.
// "Daily Notes/2026-07-27.md" -> "Daily-Notes/2026-07-27".
// A trailing "index.md" collapses onto its folder: "Food/index.md" -> "Food".
// Case is preserved (matching Quartz); link resolution is case-insensitive
// separately.
func Slugify(rel string) string {
	rel = strings.TrimSuffix(rel, ".md")
	segs := strings.Split(rel, "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		s := slugSegment(seg)
		if s != "" {
			out = append(out, s)
		}
	}
	if n := len(out); n > 0 && out[n-1] == "index" {
		out = out[:n-1]
	}
	return strings.Join(out, "/")
}

func slugSegment(seg string) string {
	seg = strings.TrimSpace(seg)
	var b strings.Builder
	b.Grow(len(seg))
	lastDash := false
	for _, r := range seg {
		switch {
		case r == ' ' || r == '\t':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		case r == '&':
			b.WriteString("-and-")
			lastDash = true
		case r == '?' || r == '#' || r == '%' || r == '[' || r == ']' ||
			r == '^' || r == '|' || r == '\\' || r == '"':
			// characters that break URLs or Obsidian links: drop
		default:
			b.WriteRune(r)
			lastDash = false
		}
	}
	return strings.Trim(b.String(), "-")
}
