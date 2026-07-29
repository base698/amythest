package mcp

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/base698/amythest/internal/vault"
)

const maxAggregateFields = 20

func aggregateFrontmatter(
	v *vault.Vault,
	in aggregateFrontmatterIn,
) (aggregateFrontmatterOut, error) {
	folder, err := cleanAggregateFolder(in.Folder)
	if err != nil {
		return aggregateFrontmatterOut{}, err
	}
	if len(in.Fields) == 0 {
		return aggregateFrontmatterOut{}, fmt.Errorf("at least one field is required")
	}
	if len(in.Fields) > maxAggregateFields {
		return aggregateFrontmatterOut{}, fmt.Errorf(
			"at most %d fields may be aggregated",
			maxAggregateFields,
		)
	}

	fields := make([]string, 0, len(in.Fields))
	seen := map[string]bool{}
	out := aggregateFrontmatterOut{
		Folder: folder,
		Fields: map[string]numericAggregate{},
	}
	for _, raw := range in.Fields {
		field := strings.TrimSpace(raw)
		if field == "" {
			return aggregateFrontmatterOut{}, fmt.Errorf("field names cannot be empty")
		}
		if len(field) > 100 || strings.ContainsAny(field, "\r\n\t") {
			return aggregateFrontmatterOut{}, fmt.Errorf("invalid field name %q", field)
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		fields = append(fields, field)
		out.Fields[field] = numericAggregate{}
	}

	prefix := strings.TrimSuffix(folder, "/") + "/"
	for _, note := range v.Notes {
		if note.Path != folder+".md" && !strings.HasPrefix(note.Path, prefix) {
			continue
		}
		out.NotesScanned++
		for _, field := range fields {
			value, ok := numericValue(note.FM.Meta[field])
			if !ok {
				continue
			}
			aggregate := out.Fields[field]
			aggregate.Sum += value
			aggregate.Count++
			out.Fields[field] = aggregate
		}
	}
	if out.NotesScanned == 0 {
		return aggregateFrontmatterOut{}, fmt.Errorf(
			"no notes found beneath folder %q",
			folder,
		)
	}
	for field, aggregate := range out.Fields {
		// Keep JSON output stable and voice-friendly after summing many decimal
		// values (for example, 53.7 rather than 53.70000000000004).
		aggregate.Sum = math.Round(aggregate.Sum*1e9) / 1e9
		out.Fields[field] = aggregate
	}
	return out, nil
}

func cleanAggregateFolder(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("a vault-relative folder is required")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "" || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("folder must stay inside the vault")
	}
	return cleaned, nil
}

func numericValue(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
