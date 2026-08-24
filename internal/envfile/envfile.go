// Package envfile reads the shell-style KEY=VALUE credential file shared by
// the server unit, kanban.py, and amy (default ~/.config/amythest/env).
// Secrets live in the environment or this file — never in yaml config.
package envfile

import (
	"os"
	"strings"
)

// Parse reads a shell-style env file: blank lines and #-comments skipped,
// values unquoted. Returns an error only when the file cannot be read.
func Parse(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `'"`)
	}
	return values, nil
}

// Lookup returns the process environment's value for key, falling back to
// the env file at path; "" when neither has it.
func Lookup(path, key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	values, err := Parse(path)
	if err != nil {
		return ""
	}
	return values[key]
}
