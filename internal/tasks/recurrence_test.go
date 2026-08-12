package tasks

import (
	"strings"
	"testing"
	"time"
)

func TestValidRecurrenceCoversObsidianForms(t *testing.T) {
	valid := []string{
		"every day", "every 4 days", "every week", "every 2 weeks",
		"every month", "every year", "every weekday",
		"every week on wednesday, saturday",
		"every 4 weeks on wednesday",
		"every 4 days when done", "every two weeks when done",
	}
	for _, rule := range valid {
		if !ValidRecurrence(rule) {
			t.Fatalf("ValidRecurrence(%q) = false", rule)
		}
	}
	for _, rule := range []string{"every blue moon", "sometimes", "4 days", "every"} {
		if ValidRecurrence(rule) {
			t.Fatalf("ValidRecurrence(%q) = true", rule)
		}
	}
}

func TestEveryNWeeksOnWeekdayAdvancesWholeWeeks(t *testing.T) {
	// Wed 2026-08-12 + "every 4 weeks on wednesday" → Wed 2026-09-09.
	next, whenDone := parseRecurrence("every 4 weeks on wednesday")
	if next == nil || whenDone {
		t.Fatalf("parse failed: nil=%v whenDone=%v", next == nil, whenDone)
	}
	from := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	got := next(from)
	if got.Format("2006-01-02") != "2026-09-09" {
		t.Fatalf("next = %s", got.Format("2006-01-02"))
	}
}

func TestUpdateRecurrenceLineReplaceClearsAndInserts(t *testing.T) {
	body := []byte("Chores:\n- [ ] Shave 🔁 every week on Wednesday, Saturday 🏁 delete 📅 2026-08-12\n- [ ] water ferns 📅 2026-08-14\n- [ ] no fields task\n")

	// Replace an existing rule; 🏁 and 📅 survive.
	updated, err := UpdateRecurrenceLine(body, 2, "Shave", StatusOpen, "every week on Wednesday, Saturday", "every 4 days when done")
	if err != nil {
		t.Fatal(err)
	}
	want := "- [ ] Shave 🔁 every 4 days when done 🏁 delete 📅 2026-08-12"
	if got := strings.Split(string(updated), "\n")[1]; got != want {
		t.Fatalf("replaced line = %q, want %q", got, want)
	}

	// Insert before 📅 on a task that has no rule.
	updated, err = UpdateRecurrenceLine(body, 3, "water ferns", StatusOpen, "", "every 3 days")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(string(updated), "\n")[2]; got != "- [ ] water ferns 🔁 every 3 days 📅 2026-08-14" {
		t.Fatalf("insert-before-due line = %q", got)
	}

	// Append when no 📅 exists.
	updated, err = UpdateRecurrenceLine(body, 4, "no fields task", StatusOpen, "", "every week")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(string(updated), "\n")[3]; got != "- [ ] no fields task 🔁 every week" {
		t.Fatalf("append line = %q", got)
	}

	// Clear an existing rule.
	updated, err = UpdateRecurrenceLine(body, 2, "Shave", StatusOpen, "every week on Wednesday, Saturday", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(string(updated), "\n")[1]; got != "- [ ] Shave 🏁 delete 📅 2026-08-12" {
		t.Fatalf("cleared line = %q", got)
	}
}

func TestUpdateRecurrenceLineRejectsBadInput(t *testing.T) {
	body := []byte("- [ ] Shave 🔁 every week 📅 2026-08-12\nplain text\n")

	if _, err := UpdateRecurrenceLine(body, 1, "Shave", StatusOpen, "every week", "every blue moon"); err == nil {
		t.Fatal("expected invalid-rule error")
	}
	if _, err := UpdateRecurrenceLine(body, 1, "Shave", StatusOpen, "stale expectation", "every day"); err == nil {
		t.Fatal("expected expectation-mismatch error")
	}
	if _, err := UpdateRecurrenceLine(body, 2, "plain text", StatusOpen, "", "every day"); err == nil {
		t.Fatal("expected not-a-task error")
	}
	double := []byte("- [ ] twice 🔁 every day 🔁 every week 📅 2026-08-12\n")
	if _, err := UpdateRecurrenceLine(double, 1, "twice", StatusOpen, "every day", "every month"); err == nil {
		t.Fatal("expected multiple-rules error")
	}
}

func TestUpdateRecurrenceRoundTripSurvivesToggle(t *testing.T) {
	// Setting "every 4 days when done" then completing on day X spawns X+4.
	body := []byte("- [ ] Shave 📅 2026-08-12\n")
	withRule, err := UpdateRecurrenceLine(body, 1, "Shave", StatusOpen, "", "every 4 days when done")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC) // completed a day late
	toggled, recurred, err := ToggleLine(withRule, 1, true, now)
	if err != nil || !recurred {
		t.Fatalf("toggle: recurred=%v err=%v", recurred, err)
	}
	lines := strings.Split(string(toggled), "\n")
	// "when done": next due = completion (08-13) + 4 = 08-17, not due+4.
	if !strings.Contains(lines[0], "- [ ] Shave") || !strings.Contains(lines[0], "📅 2026-08-17") {
		t.Fatalf("next occurrence = %q", lines[0])
	}
	if !strings.Contains(lines[1], "- [x] Shave") || !strings.Contains(lines[1], "✅ 2026-08-13") {
		t.Fatalf("completed line = %q", lines[1])
	}
}
