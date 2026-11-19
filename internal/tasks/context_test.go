package tasks

import (
	"strings"
	"testing"
)

func TestContextStaysWithExactTaskAndPurge(t *testing.T) {
	body := "- [ ] Before\n- [ ] Investigate\n  - user child\n- [ ] After\n"
	ts, _, _ := ParseFile("n", "n.md", []byte(body))
	updated, e := Annotate(body, ts[1], "Review (claude): Evidence in PR 123\nSource: https://example.test/pull/123\n- [x] untrusted checkbox")
	if e != nil {
		t.Fatal(e)
	}
	parsed, _, _ := ParseFile("n", "n.md", []byte(updated))
	if len(parsed) != 3 || len(parsed[0].Context) != 0 || len(parsed[1].Context) != 3 || len(parsed[2].Context) != 0 {
		t.Fatalf("context leaked: %+v", parsed)
	}
	if _, e = Annotate("changed\n"+body, ts[1], "wrong"); e == nil {
		t.Fatal("shifted task accepted")
	}
	updated = strings.Replace(updated, "- [ ] Investigate", "- [-] Investigate", 1)
	purged, e := purgeCancelledBody([]byte(updated), []int{2})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(purged), "PR 123") || !strings.Contains(string(purged), "  - user child") || !strings.Contains(string(purged), "- [ ] After") {
		t.Fatalf("purge context: %s", purged)
	}
}

func TestPurgeNeverCrossesOtherTaskForUnclosedContext(t *testing.T) {
	body := "- [-] Old\n  > <!-- amy-context -->\n  > unfinished\n- [ ] Keep\n  > <!-- /amy-context -->\n"
	out, e := purgeCancelledBody([]byte(body), []int{1})
	if e != nil {
		t.Fatal(e)
	}
	if string(out) != strings.TrimPrefix(body, "- [-] Old\n") {
		t.Fatal("deleted unrelated task", string(out))
	}
	crlf := "- [-] Old\r\n  > <!-- amy-context -->\r\n  > evidence\r\n  > <!-- /amy-context -->\r\n- [ ] Keep\r\n"
	out, e = purgeCancelledBody([]byte(crlf), []int{1})
	if e != nil || string(out) != "- [ ] Keep\r\n" {
		t.Fatal("CRLF context", e, string(out))
	}
}
