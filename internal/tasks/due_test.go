package tasks

import (
	"strings"
	"testing"
)

func TestUpdateDueDateLineSetsDueDate(t *testing.T) {
	body := []byte("# Project\n- [ ] Ship release #backlog\n")

	got, err := UpdateDueDateLine(body, 2, "Ship release #backlog", StatusOpen, "", "2026-08-15")
	if err != nil {
		t.Fatal(err)
	}
	if want := "- [ ] Ship release #backlog 📅 2026-08-15"; !strings.Contains(string(got), want) {
		t.Fatalf("updated body missing %q:\n%s", want, got)
	}
}

func TestUpdateDueDateLineChangesDueDate(t *testing.T) {
	body := []byte("- [ ] Ship release 📅 2026-08-15 ⏳ 2026-08-10\n")

	got, err := UpdateDueDateLine(body, 1, "Ship release", StatusOpen, "2026-08-15", "2026-08-20")
	if err != nil {
		t.Fatal(err)
	}
	if want := "- [ ] Ship release 📅 2026-08-20 ⏳ 2026-08-10"; !strings.Contains(string(got), want) {
		t.Fatalf("updated body missing %q:\n%s", want, got)
	}
}

func TestUpdateDueDateLineClearsDueDate(t *testing.T) {
	body := []byte("- [ ] Ship release 📅 2026-08-15 ⏳ 2026-08-10\n")

	got, err := UpdateDueDateLine(body, 1, "Ship release", StatusOpen, "2026-08-15", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "📅") {
		t.Fatalf("due date was not cleared:\n%s", got)
	}
	if !strings.Contains(string(got), "- [ ] Ship release ⏳ 2026-08-10") {
		t.Fatalf("other task metadata changed:\n%s", got)
	}
}

func TestUpdateDueDateLineRejectsStaleTask(t *testing.T) {
	body := []byte("- [ ] Renamed task 📅 2026-08-15\n")

	if _, err := UpdateDueDateLine(body, 1, "Old task name", StatusOpen, "2026-08-15", "2026-08-20"); err == nil {
		t.Fatal("stale task text was accepted")
	}
	if _, err := UpdateDueDateLine(body, 1, "Renamed task", StatusOpen, "2026-08-14", "2026-08-20"); err == nil {
		t.Fatal("stale due date was accepted")
	}
}

func TestUpdateDueDateLineRejectsStaleTaskStatus(t *testing.T) {
	body := []byte("- [x] Ship release 📅 2026-08-15\n")

	if _, err := UpdateDueDateLine(body, 1, "Ship release", StatusOpen, "2026-08-15", "2026-08-20"); err == nil {
		t.Fatal("stale task status was accepted")
	}
}

func TestUpdateDueDateLineRejectsInvalidDate(t *testing.T) {
	body := []byte("- [ ] Ship release\n")
	for _, invalid := range []string{"2026-02-30", "08/15/2026", "2026-8-5"} {
		if _, err := UpdateDueDateLine(body, 1, "Ship release", StatusOpen, "", invalid); err == nil {
			t.Fatalf("invalid date %q was accepted", invalid)
		}
	}
}

func TestUpdateDueDateLineRejectsAmbiguousDuplicateDueMetadata(t *testing.T) {
	body := []byte("- [ ] Ship release 📅 2026-08-15 ⏳ 2026-08-10 📅 2026-08-16\n")

	if _, err := UpdateDueDateLine(body, 1, "Ship release", StatusOpen, "2026-08-15", "2026-08-20"); err == nil {
		t.Fatal("ambiguous duplicate due metadata was accepted")
	}
}

func TestUpdateDueDateLineSetsDueDatePreservingCRLF(t *testing.T) {
	body := []byte("# Project\r\n- [ ] Ship release\r\n")

	got, err := UpdateDueDateLine(body, 2, "Ship release", StatusOpen, "", "2026-08-15")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Project\r\n- [ ] Ship release 📅 2026-08-15\r\n"
	if string(got) != want {
		t.Fatalf("CRLF set mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestUpdateDueDateLineChangesDueDatePreservingCRLF(t *testing.T) {
	body := []byte("# Project\r\n- [ ] Ship release 📅 2026-08-15 ⏳ 2026-08-10\r\n")

	got, err := UpdateDueDateLine(body, 2, "Ship release", StatusOpen, "2026-08-15", "2026-08-20")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Project\r\n- [ ] Ship release 📅 2026-08-20 ⏳ 2026-08-10\r\n"
	if string(got) != want {
		t.Fatalf("CRLF change mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestUpdateDueDateLineClearsDueDatePreservingCRLF(t *testing.T) {
	body := []byte("# Project\r\n- [ ] Ship release 📅 2026-08-15 ⏳ 2026-08-10\r\n")

	got, err := UpdateDueDateLine(body, 2, "Ship release", StatusOpen, "2026-08-15", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Project\r\n- [ ] Ship release ⏳ 2026-08-10\r\n"
	if string(got) != want {
		t.Fatalf("CRLF clear mismatch:\n got %q\nwant %q", got, want)
	}
}
