package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestJSONFormatProducesOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("hello", "k", "v")
	logger.Warn("world")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("not json: %q", line)
		}
		for _, key := range []string{"time", "level", "msg"} {
			if _, ok := obj[key]; !ok {
				t.Fatalf("missing %q in %q", key, line)
			}
		}
	}
}

func TestSetupHonorsLevelAndFallsBackOnJunk(t *testing.T) {
	prev := slog.Default()
	defer slog.SetDefault(prev)

	logger := Setup(Config{Level: "warn"})
	if logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("warn level must suppress info")
	}
	if !logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("warn level must allow warn")
	}
	// Junk values fall back to safe defaults instead of failing.
	logger = Setup(Config{Level: "shouty", Format: "yaml", Output: "pipe"})
	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("fallback level must be info")
	}
}

func TestWrapSeamIsApplied(t *testing.T) {
	prev := slog.Default()
	defer slog.SetDefault(prev)
	prevWrap := Wrap
	defer func() { Wrap = prevWrap }()

	called := false
	Wrap = func(h slog.Handler) slog.Handler {
		called = true
		return h
	}
	Setup(Config{})
	if !called {
		t.Fatal("Wrap seam was not applied")
	}
}

func TestContextCarriesLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil)).With("request_id", "abc123")
	ctx := IntoContext(context.Background(), logger)
	From(ctx).Info("inside")
	if !strings.Contains(buf.String(), "request_id=abc123") {
		t.Fatalf("context logger lost attrs: %q", buf.String())
	}
	if From(context.Background()) != slog.Default() {
		t.Fatal("empty context must fall back to default")
	}
}

func TestRequestIDsAreUniqueHex(t *testing.T) {
	a, b := NewRequestID(), NewRequestID()
	if a == b || len(a) != 16 {
		t.Fatalf("ids = %q %q", a, b)
	}
}
