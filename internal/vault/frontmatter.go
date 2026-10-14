package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

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

// Property is one JSON-safe frontmatter entry. Type lets clients distinguish
// strings from booleans, numbers, lists, objects, nulls, and YAML timestamps
// without interpreting display text. Properties are always emitted in key
// order so every client presents a stable list.
type Property struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Value any    `json:"value"`
	Text  string `json:"text"`
}

func (fm Frontmatter) Properties() []Property {
	if len(fm.Meta) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fm.Meta))
	for key := range fm.Meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	properties := make([]Property, 0, len(keys))
	for _, key := range keys {
		kind, value := propertyValue(fm.Meta[key])
		properties = append(properties, Property{Key: key, Type: kind, Value: value, Text: propertyText(kind, value)})
	}
	return properties
}

func propertyText(kind string, value any) string {
	if kind == "string" || kind == "date" || kind == "number" {
		return fmt.Sprint(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "(unavailable)"
	}
	return string(encoded)
}

func propertyValue(value any) (string, any) {
	switch value := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return "bool", value
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number", value
	case float32:
		return "number", jsonSafeFloat(float64(value), value)
	case float64:
		return "number", jsonSafeFloat(value, value)
	case string:
		return "string", value
	case time.Time:
		return "date", value.Format(time.RFC3339)
	case []any:
		items := make([]any, len(value))
		for i := range value {
			_, items[i] = propertyValue(value[i])
		}
		return "list", items
	case map[string]any:
		object := make(map[string]any, len(value))
		for key, item := range value {
			_, object[key] = propertyValue(item)
		}
		return "object", object
	default:
		// yaml.v3 should not produce other values when decoding into any, but
		// stringifying keeps the API encodable if that contract ever changes.
		return "string", fmt.Sprint(value)
	}
}

func jsonSafeFloat(value float64, finite any) any {
	switch {
	case math.IsNaN(value):
		return ".nan"
	case math.IsInf(value, 1):
		return ".inf"
	case math.IsInf(value, -1):
		return "-.inf"
	default:
		return finite
	}
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
