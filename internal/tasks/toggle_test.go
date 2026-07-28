package tasks

import (
	"strings"
	"testing"
	"time"
)

var toggleNow = time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)

func TestToggleComplete(t *testing.T) {
	body := "# Note\n- [ ] Buy milk 📅 2026-08-01\n- [x] Done already ✅ 2026-07-01\n"
	out, recurred, err := ToggleLine([]byte(body), 2, true, toggleNow)
	if err != nil || recurred {
		t.Fatalf("err=%v recurred=%v", err, recurred)
	}
	if !strings.Contains(string(out), "- [x] Buy milk 📅 2026-08-01 ✅ 2026-07-28") {
		t.Errorf("out = %q", out)
	}
}

func TestToggleUncomplete(t *testing.T) {
	body := "- [x] Ship it 📅 2026-08-01 ✅ 2026-07-28\n"
	out, _, err := ToggleLine([]byte(body), 1, false, toggleNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "- [ ] Ship it 📅 2026-08-01\n"
	if string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestToggleRecurringSpawnsNextOccurrence(t *testing.T) {
	body := "- [ ] Water plants 🔁 every week 📅 2026-07-30\n"
	out, recurred, err := ToggleLine([]byte(body), 1, true, toggleNow)
	if err != nil || !recurred {
		t.Fatalf("err=%v recurred=%v out=%q", err, recurred, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	if lines[0] != "- [ ] Water plants 🔁 every week 📅 2026-08-06" {
		t.Errorf("new occurrence = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "- [x] Water plants 🔁 every week 📅 2026-07-30 ✅ 2026-07-28") {
		t.Errorf("completed = %q", lines[1])
	}
}

func TestToggleRecurringWhenDone(t *testing.T) {
	body := "- [ ] Change filter 🔁 every 2 weeks when done 📅 2026-07-01\n"
	out, recurred, err := ToggleLine([]byte(body), 1, true, toggleNow)
	if err != nil || !recurred {
		t.Fatalf("err=%v recurred=%v", err, recurred)
	}
	// Base is today (2026-07-28) + 14 days = 2026-08-11; delta applies to due.
	if !strings.Contains(string(out), "📅 2026-08-11") {
		t.Errorf("out = %q", out)
	}
}

func TestToggleRecurringWeekdayList(t *testing.T) {
	// 2026-07-30 is a Thursday; next "wednesday, saturday" is Aug 1 (Sat).
	body := "- [ ] Shave 🔁 every week on Wednesday, Saturday 📅 2026-07-30\n"
	out, recurred, err := ToggleLine([]byte(body), 1, true, toggleNow)
	if err != nil || !recurred {
		t.Fatalf("err=%v recurred=%v", err, recurred)
	}
	if !strings.Contains(strings.Split(string(out), "\n")[0], "📅 2026-08-01") {
		t.Errorf("out = %q", out)
	}
}

func TestToggleRejectsNonTaskLine(t *testing.T) {
	if _, _, err := ToggleLine([]byte("# heading\n- [ ] task\n"), 1, true, toggleNow); err == nil {
		t.Error("expected error for non-task line")
	}
	if _, _, err := ToggleLine([]byte("- [ ] task\n"), 5, true, toggleNow); err == nil {
		t.Error("expected error for out-of-range line")
	}
}
