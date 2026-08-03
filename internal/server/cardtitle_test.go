package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCardTitleFromTaskFitsTheBoardLimit(t *testing.T) {
	// The wording that failed with "title must be 1-200 characters": 205
	// characters, and the "Ü" costs two bytes so it overruns further.
	long := "Justin: before snapshot retirement, decide whether to restart the old Bible tracker, " +
		"preserve the Stanford cybersecurity-program link, or carry forward the Ryan-family/KÜHL " +
		"reminders from Unsorted TODOs.md."
	if len(long) <= cardTitleLimit {
		t.Fatalf("fixture must exceed the limit, got %d bytes", len(long))
	}
	title, truncated := cardTitleFromTask(long)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(title) > cardTitleLimit {
		t.Fatalf("title is %d bytes, over the %d limit: %q", len(title), cardTitleLimit, title)
	}
	if !utf8.ValidString(title) {
		t.Fatalf("truncation split a multi-byte rune: %q", title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("truncated title should signal it was cut: %q", title)
	}
	if !strings.HasPrefix(title, "Justin: before snapshot retirement") {
		t.Fatalf("title lost its beginning: %q", title)
	}
	if strings.HasSuffix(strings.TrimSuffix(title, "…"), " ") {
		t.Fatalf("trailing space before the ellipsis: %q", title)
	}
}

func TestCardTitleFromTaskLeavesShortTitlesAlone(t *testing.T) {
	for _, in := range []string{"Ship release", "Rename the KÜHL folder"} {
		got, truncated := cardTitleFromTask(in)
		if truncated || got != in {
			t.Fatalf("cardTitleFromTask(%q) = %q, %v", in, got, truncated)
		}
	}
}

func TestCardTitleFromTaskCollapsesWhitespaceToOneLine(t *testing.T) {
	got, _ := cardTitleFromTask("Ship  the\trelease\nnow")
	if got != "Ship the release now" {
		t.Fatalf("got %q", got)
	}
}

func TestCardTitleFromTaskHandlesAllMultibyteText(t *testing.T) {
	// No spaces at all, every rune multi-byte: the word-boundary search must
	// not fire and the cut must still land on a rune boundary.
	title, truncated := cardTitleFromTask(strings.Repeat("é", 300))
	if !truncated || len(title) > cardTitleLimit || !utf8.ValidString(title) {
		t.Fatalf("title=%q bytes=%d valid=%v", title, len(title), utf8.ValidString(title))
	}
}
