package tasks

import (
	"strings"
	"testing"
)

func TestSetFrontmatterScalar(t *testing.T) {
	src := []byte("---\ntitle: Widget\nweight: 2\n---\nBody text.\n")

	// update an existing key with float inference
	out, err := setFrontmatterScalar(src, "weight", scalarValueNode("0.8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "weight: 0.8") || !strings.Contains(string(out), "title: Widget") {
		t.Errorf("update = %q", out)
	}
	if !strings.HasSuffix(string(out), "Body text.\n") {
		t.Errorf("body altered: %q", out)
	}

	// add a new key with string typing
	out, err = setFrontmatterScalar(src, "status", scalarValueNode("in review"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "status: in review") {
		t.Errorf("add = %q", out)
	}

	// bool inference
	out, err = setFrontmatterScalar(src, "complex", scalarValueNode("true"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "complex: true") {
		t.Errorf("bool = %q", out)
	}

	// no-op when the value already matches
	same, err := setFrontmatterScalar(src, "weight", scalarValueNode("2"))
	if err != nil {
		t.Fatal(err)
	}
	if string(same) != string(src) {
		t.Errorf("expected no-op, got %q", same)
	}

	// note without frontmatter gains a block
	out, err = setFrontmatterScalar([]byte("Just a body.\n"), "rating", scalarValueNode("5"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "---\nrating: 5\n---\n") || !strings.HasSuffix(string(out), "Just a body.\n") {
		t.Errorf("new frontmatter = %q", out)
	}
}
