package vault

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the result of a never-fail frontmatter parse. When the YAML
// is malformed (the vault has hundreds of legacy files with broken YAML that
// crashed Quartz), Meta is empty, Err records the problem, and Raw preserves
// the original text so nothing is lost.
type Frontmatter struct {
	Meta map[string]any
	Raw  string
	Err  error
}

var fmDelim = []byte("---")

// ParseFrontmatter splits an optional leading `---` YAML block from src and
// parses it. It never panics and never returns a parse failure as a hard
// error: the body is always usable. If a frontmatter block is present but
// malformed it is still stripped from the body.
func ParseFrontmatter(src []byte) (fm Frontmatter, body []byte) {
	body = src
	rest, ok := bytes.CutPrefix(src, fmDelim)
	if !ok {
		return fm, body
	}
	// Delimiter line must be exactly "---" (allow trailing \r).
	rest, ok = bytes.CutPrefix(bytes.TrimPrefix(rest, []byte("\r")), []byte("\n"))
	if !ok {
		return fm, body
	}
	end := findClosingDelim(rest)
	if end < 0 {
		return fm, body
	}
	block := rest[:end]
	after := rest[end:]
	// Skip the closing delimiter line.
	if i := bytes.IndexByte(after, '\n'); i >= 0 {
		after = after[i+1:]
	} else {
		after = nil
	}

	fm.Raw = string(block)
	fm.Meta = map[string]any{}
	fm.Err = safeUnmarshal(block, &fm.Meta)
	if fm.Err != nil {
		fm.Meta = map[string]any{}
	}
	return fm, after
}

// findClosingDelim returns the offset in b where a line consisting of "---"
// (or "...") starts, or -1.
func findClosingDelim(b []byte) int {
	off := 0
	for off <= len(b) {
		lineEnd := bytes.IndexByte(b[off:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = b[off:]
			lineEnd = len(b) - off
		} else {
			line = b[off : off+lineEnd]
		}
		trimmed := bytes.TrimRight(line, "\r \t")
		if bytes.Equal(trimmed, []byte("---")) || bytes.Equal(trimmed, []byte("...")) {
			return off
		}
		off += lineEnd + 1
	}
	return -1
}

func safeUnmarshal(block []byte, into *map[string]any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("yaml panic: %v", r)
		}
	}()
	return yaml.Unmarshal(block, into)
}

// MetaString returns a frontmatter value as a string if it is one.
func (fm Frontmatter) MetaString(key string) string {
	if v, ok := fm.Meta[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// MetaStrings returns a frontmatter value as a string list, accepting both a
// single string and a YAML sequence (mixed types are stringified).
func (fm Frontmatter) MetaStrings(key string) []string {
	v, ok := fm.Meta[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			} else if e != nil {
				out = append(out, fmt.Sprint(e))
			}
		}
		return out
	}
	return nil
}
