package collect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Example", "GIT_AUTHOR_EMAIL=example@example.com", "GIT_COMMITTER_NAME=Example", "GIT_COMMITTER_EMAIL=example@example.com")
	out, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %s %v", args, out, e)
	}
	return strings.TrimSpace(string(out))
}
func TestCommitPatchUsesExactCommitNotWorkingTree(t *testing.T) {
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("first version\n"), 0600)
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-m", "Initial version")
	root := gitFixture(t, dir, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("second version\n"), 0600)
	gitFixture(t, dir, "commit", "-am", "Update version")
	sha := gitFixture(t, dir, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("uncommitted content\n"), 0600)
	patch, e := CommitPatch(dir, sha)
	if e != nil || !strings.Contains(string(patch), "-first version") || !strings.Contains(string(patch), "+second version") || strings.Contains(string(patch), "uncommitted content") {
		t.Fatal(string(patch), e)
	}
	patch, e = CommitPatch(dir, root)
	if e != nil || !strings.Contains(string(patch), "new file mode") {
		t.Fatal(string(patch), e)
	}
	for _, bad := range []string{"HEAD", "--output=somewhere", sha + "^", "../../file"} {
		if _, e = CommitPatch(dir, bad); e == nil {
			t.Fatal("accepted non-object ID", bad)
		}
	}
}
func TestCommitPatchMergeAndLimit(t *testing.T) {
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0600)
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-m", "Base")
	gitFixture(t, dir, "checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("new feature\n"), 0600)
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-m", "Feature")
	gitFixture(t, dir, "checkout", "main")
	gitFixture(t, dir, "merge", "--no-ff", "feature", "-m", "Merge feature")
	patch, e := CommitPatch(dir, gitFixture(t, dir, "rev-parse", "HEAD"))
	if e != nil || !strings.Contains(string(patch), "+new feature") {
		t.Fatal(string(patch), e)
	}
	os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("some added text\n", 40000)), 0600)
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-m", "Large change")
	patch, e = CommitPatch(dir, gitFixture(t, dir, "rev-parse", "HEAD"))
	if e != nil || len(patch) > CommitPatchLimit+200 || !strings.Contains(string(patch), "Diff truncated at 256 KiB") {
		t.Fatal(len(patch), e)
	}
}
