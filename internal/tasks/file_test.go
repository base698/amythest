package tasks

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestHideFileFromTasksAddsFrontmatterPreservingBodyModeAndCRLF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("# Project\r\n\r\n- [ ] Ship release\r\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := HideFileFromTasks(root, "Project.md", FileVersion(original)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\r\ntasks: false\r\n---\r\n" + string(original)
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestHideFileFromTasksAcceptsEmptyAndCommentOnlyFrontmatter(t *testing.T) {
	for name, original := range map[string][]byte{
		"empty":   []byte("---\n---\n- [ ] Task\n"),
		"comment": []byte("---\n# keep this comment\n---\n- [ ] Task\n"),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Project.md")
			if err := os.WriteFile(path, original, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := HideFileFromTasks(root, "Project.md", FileVersion(original)); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "tasks: false") || !strings.Contains(string(got), "- [ ] Task") {
				t.Fatalf("valid empty frontmatter was not updated safely: %q", got)
			}
			if name == "comment" && !strings.Contains(string(got), "# keep this comment") {
				t.Fatalf("frontmatter comment was lost: %q", got)
			}
		})
	}
}

func TestHideFileFromTasksUpdatesCanonicalKeyWithoutDuplicateAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("---\ntitle: Project\ntasks: true\nTasks: keep-me\ntags: [work]\n---\n- [ ] Ship release\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := HideFileFromTasks(root, "Project.md", FileVersion(original)); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if strings.Count(string(first), "tasks: false") != 1 || !strings.Contains(string(first), "Tasks: keep-me") || !strings.Contains(string(first), "tags: [work]") {
		t.Fatalf("frontmatter not preserved: %s", first)
	}
	if err := HideFileFromTasks(root, "Project.md", FileVersion(first)); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(second) != string(first) {
		t.Fatalf("idempotent hide changed content:\nfirst=%q\nsecond=%q", first, second)
	}
	if err := HideFileFromTasks(root, "Project.md", FileVersion(original)); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale idempotent request error=%v", err)
	}
}

func TestHideFileFromTasksRejectsMalformedFrontmatterWithoutMutation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Broken.md")
	for _, original := range [][]byte{
		[]byte("---\ntitle: [broken\n---\n- [ ] Task\n"),
		[]byte("---\ntitle: never closed\n- [ ] Task\n"),
	} {
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := HideFileFromTasks(root, "Broken.md", FileVersion(original)); err == nil || !strings.Contains(err.Error(), "frontmatter") {
			t.Fatalf("expected malformed frontmatter rejection, got %v", err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != string(original) {
			t.Fatalf("malformed note mutated: %q", got)
		}
	}
}

func TestUpdateDueDateInFilePreservesFrontmatter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := "---\ntype: project\n---\n- [ ] Ship release 📅 2026-08-15\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := UpdateDueDateInFile(root, "Project.md", 1, "Ship release", StatusOpen, "2026-08-15", "2026-08-20"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "---\ntype: project\n---\n- [ ] Ship release 📅 2026-08-20\n"; string(got) != want {
		t.Fatalf("updated note mismatch:\n%s", got)
	}
}

func TestVaultOperationsRejectSymlinkedRootPath(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "note.md"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	if release, err := acquireTaskVaultWriteLock(alias); err == nil {
		release()
		t.Fatal("writer accepted a symlinked vault root")
	}
	if _, err := ReadNoteFile(alias, "note.md"); err == nil {
		t.Fatal("reader accepted a symlinked vault root")
	}
}

func TestTaskVaultWriteLockHelperProcess(t *testing.T) {
	root := os.Getenv("AMYTHEST_LOCK_TEST_ROOT")
	if root == "" {
		return
	}
	fmt.Fprintln(os.Stdout, "ATTEMPT")
	release, err := acquireTaskVaultWriteLock(root)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "LOCKED")
	_, _ = os.Stdin.Read(make([]byte, 1))
	release()
}

func TestTaskVaultWriteLockSerializesProcesses(t *testing.T) {
	root := t.TempDir()
	start := func() (*exec.Cmd, *bufio.Scanner, func()) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTaskVaultWriteLockHelperProcess$")
		cmd.Env = append(os.Environ(), "AMYTHEST_LOCK_TEST_ROOT="+root)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, bufio.NewScanner(stdout), func() { _ = stdin.Close() }
	}

	first, firstOut, releaseFirst := start()
	if !firstOut.Scan() || firstOut.Text() != "ATTEMPT" || !firstOut.Scan() || firstOut.Text() != "LOCKED" {
		t.Fatalf("first helper did not acquire lock: %q", firstOut.Text())
	}
	second, secondOut, releaseSecond := start()
	if !secondOut.Scan() || secondOut.Text() != "ATTEMPT" {
		t.Fatalf("second helper did not reach lock attempt: %q", secondOut.Text())
	}
	secondLocked := make(chan bool, 1)
	go func() { secondLocked <- secondOut.Scan() && secondOut.Text() == "LOCKED" }()
	select {
	case <-secondLocked:
		t.Fatal("second process acquired the vault lock before release")
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-secondLocked:
		if !ok {
			t.Fatal("second helper exited without acquiring the lock")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second process did not acquire released lock")
	}
	releaseSecond()
	if err := second.Wait(); err != nil {
		t.Fatal(err)
	}
}

// ToggleInFile is shared by the web UI's HTTP handler and the MCP toggle_task
// tool, so it is covered directly rather than only through ToggleLine.
func TestWithTaskVaultWriteLockSerializesWriters(t *testing.T) {
	root := t.TempDir()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		release, err := acquireTaskVaultWriteLock(root)
		if err != nil {
			firstDone <- err
			return
		}
		close(firstEntered)
		<-releaseFirst
		release()
		firstDone <- nil
	}()
	select {
	case <-firstEntered:
	case err := <-firstDone:
		t.Fatalf("first writer failed to acquire lock: %v", err)
	}

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		release, err := acquireTaskVaultWriteLock(root)
		if err != nil {
			secondDone <- err
			return
		}
		close(secondEntered)
		release()
		secondDone <- nil
	}()
	select {
	case <-secondEntered:
		t.Fatal("second writer entered while the first writer still held the lock")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second writer did not acquire the released lock")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestRollbackTaskExchangePreservesASecondConcurrentEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	if err := os.WriteFile(path, []byte("external-before"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootFD, release, err := acquireTaskVaultWriteLockFD(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	parentFD, leaf, err := openParentAt(rootFD, "note.md", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(parentFD)
	tmp, err := createTempAt(parentFD, leaf, []byte("our-update"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Renameat2(parentFD, tmp, parentFD, leaf, unix.RENAME_EXCHANGE); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external-after"), 0o644); err != nil {
		t.Fatal(err)
	}
	preserved, err := rollbackTaskExchangeAt(parentFD, leaf, tmp, []byte("our-update"))
	if err == nil || preserved == "" {
		t.Fatalf("expected second edit to be preserved, path=%q err=%v", preserved, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "external-before" {
		t.Fatalf("pre-exchange edit not restored: %q", got)
	}
	got, _, readErr := readRegularAt(parentFD, preserved)
	if readErr != nil || string(got) != "external-after" {
		t.Fatalf("post-exchange edit not preserved: %q err=%v", got, readErr)
	}
	_ = unix.Unlinkat(parentFD, preserved, 0)
}

func TestReplaceTaskFileRejectsConcurrentEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	expected := []byte("- [ ] Original\n")
	concurrent := []byte("owner: changed elsewhere\n- [ ] Original\n")
	if err := os.WriteFile(path, concurrent, 0o640); err != nil {
		t.Fatal(err)
	}

	rootFD, release, err := acquireTaskVaultWriteLockFD(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	parentFD, leaf, err := openParentAt(rootFD, "note.md", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(parentFD)
	err = replaceTaskFileAt(parentFD, leaf, expected, []byte("- [ ] Original #backlog\n"), 0o640)
	if err == nil {
		t.Fatal("expected a stale source version to be rejected")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(concurrent) {
		t.Fatalf("concurrent edit was overwritten: %q", got)
	}
}

func TestReplaceExistingNoteFileRejectsEditAfterReadAndPreservesIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	expected := []byte("original")
	external := []byte("external edit")
	if err := os.WriteFile(path, expected, 0o640); err != nil {
		t.Fatal(err)
	}
	rootFD, release, err := acquireTaskVaultWriteLockFD(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	parentFD, leaf, err := openParentAt(rootFD, "note.md", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(parentFD)
	if err := os.WriteFile(path, external, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := replaceExistingNoteFileAt(parentFD, leaf, expected, []byte("our overwrite"), 0o640); err == nil {
		t.Fatal("expected overwrite to reject the edit made after its read")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(external) {
		t.Fatalf("external edit was not preserved: %q err=%v", got, err)
	}
}

func TestWriteNoteFileUsesSharedSafeAtomicPath(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNoteFile(root, "escape/note.md", []byte("no"), true); err == nil {
		t.Fatal("expected directory symlink rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "note.md")); !os.IsNotExist(err) {
		t.Fatalf("wrote outside vault: %v", err)
	}

	note := filepath.Join(root, "note.md")
	victim := filepath.Join(parent, "victim")
	if err := os.WriteFile(note, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, note+".amythest-tmp"); err != nil {
		t.Fatal(err)
	}
	if err := WriteNoteFile(root, "note.md", []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(note); string(got) != "new" {
		t.Fatalf("note=%q", got)
	}
	if got, _ := os.ReadFile(victim); string(got) != "safe" {
		t.Fatalf("predictable temp symlink victim changed: %q", got)
	}
}

func TestMoveFileInVaultRejectsMalformedVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Old.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := MoveFileInVault(root, "Old.md", FileDispositionArchive, strings.Repeat("z", 64))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected version validation error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Old.md")); err != nil {
		t.Fatalf("source changed after malformed request: %v", err)
	}
}

func TestMoveFileInVaultArchivesAndPreservesRelativePath(t *testing.T) {
	root := t.TempDir()
	source := "Projects/Old.md"
	if err := os.MkdirAll(filepath.Join(root, "Projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Old\n\n- [ ] Retire this\n")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(source)), content, 0o640); err != nil {
		t.Fatal(err)
	}
	version := FileVersion(content)

	destination, err := MoveFileInVault(root, source, FileDispositionArchive, version)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Archive/Deleted/Projects/Old.md"; destination != want {
		t.Fatalf("destination=%q want=%q", destination, want)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(source))); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(destination)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content changed: %q", got)
	}
}

func TestMoveFileInVaultTrashRejectsCollisionAndStaleVersion(t *testing.T) {
	root := t.TempDir()
	source := "Old.md"
	content := []byte("# Old\n")
	if err := os.WriteFile(filepath.Join(root, source), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveFileInVault(root, source, FileDispositionTrash, FileVersion([]byte("stale"))); err == nil {
		t.Fatal("expected stale version rejection")
	}
	if _, err := os.Stat(filepath.Join(root, source)); err != nil {
		t.Fatalf("stale rejection moved source: %v", err)
	}

	destination := filepath.Join(root, ".trash", "Amythest", source)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveFileInVault(root, source, FileDispositionTrash, FileVersion(content)); err == nil {
		t.Fatal("expected destination collision rejection")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("destination overwritten: %q", got)
	}
}

func TestMoveFileInVaultRejectsSourceSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveFileInVault(root, "link.md", FileDispositionArchive, FileVersion([]byte("outside"))); err == nil {
		t.Fatal("expected source symlink rejection")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "outside" {
		t.Fatalf("outside file changed: %q err=%v", got, err)
	}
}

func TestToggleInFilePreservesMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mode.md")
	if err := os.WriteFile(path, []byte("- [ ] Task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o664); err != nil {
		t.Fatal(err)
	}
	if _, err := ToggleInFile(root, "mode.md", 1, FileVersion([]byte("- [ ] Task\n")), true, triageNow); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o664 {
		t.Fatalf("mode=%#o want=%#o", got, 0o664)
	}
}

func TestToggleAndReindexHoldVaultLockAsOneCriticalSection(t *testing.T) {
	root := t.TempDir()
	content := []byte("- [ ] Task\n")
	if err := os.WriteFile(filepath.Join(root, "note.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	reindexEntered := make(chan struct{})
	releaseReindex := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		_, err := ToggleInFileAndReindex(root, "note.md", 1, FileVersion(content), true, triageNow, func() error {
			close(reindexEntered)
			<-releaseReindex
			return nil
		})
		mutationDone <- err
	}()
	<-reindexEntered

	secondEntered := make(chan struct{})
	go func() {
		release, err := acquireTaskVaultWriteLock(root)
		if err == nil {
			close(secondEntered)
			release()
		}
	}()
	select {
	case <-secondEntered:
		t.Fatal("another writer entered before synchronous reindex completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseReindex)
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("writer did not enter after reindex completed")
	}
}

func TestToggleInFileCompletesAndRecurs(t *testing.T) {
	root := t.TempDir()
	rel := "Chores.md"
	original := "# Chores\n\n- [ ] Water the plants 🔁 every week 📅 2026-07-01\n- [ ] Buy milk\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	recurred, err := ToggleInFile(root, rel, 3, FileVersion([]byte(original)), true, now)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if !recurred {
		t.Fatal("a 🔁 task must report recurred")
	}

	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, "[x] Water the plants") {
		t.Errorf("task was not completed:\n%s", got)
	}
	if !strings.Contains(got, "✅ 2026-07-29") {
		t.Errorf("missing done-date:\n%s", got)
	}
	// The next occurrence is inserted unchecked, so the task never disappears
	// from the vault just because this week's was ticked off.
	if strings.Count(got, "Water the plants") != 2 {
		t.Errorf("expected the next occurrence to be inserted:\n%s", got)
	}
	if !strings.HasPrefix(got, "# Chores\n") {
		t.Errorf("content above the task was disturbed:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] Buy milk") {
		t.Errorf("an unrelated task was modified:\n%s", got)
	}
	if strings.Contains(got, ".amythest-tmp") {
		t.Error("temp file leaked into the note")
	}
}

func TestToggleInFileRejectsStaleVersionAfterTaskLineShift(t *testing.T) {
	root := t.TempDir()
	rel := "Chores.md"
	original := []byte("- [ ] Water plants 🔁 every week\n- [ ] Buy milk\n")
	if err := os.WriteFile(filepath.Join(root, rel), original, 0o644); err != nil {
		t.Fatal(err)
	}
	shifted := []byte("- [ ] New first task\n- [ ] Water plants 🔁 every week\n- [ ] Buy milk\n")
	if err := os.WriteFile(filepath.Join(root, rel), shifted, 0o644); err != nil {
		t.Fatal(err)
	}
	recurred, err := ToggleInFile(root, rel, 1, FileVersion(original), true, triageNow)
	if err == nil {
		t.Fatal("expected stale task version rejection")
	}
	if recurred {
		t.Fatal("stale toggle must not report or create a recurrence")
	}
	if got, readErr := os.ReadFile(filepath.Join(root, rel)); readErr != nil || string(got) != string(shifted) {
		t.Fatalf("stale toggle changed another task: %q err=%v", got, readErr)
	}
}

// Line numbers are BODY-relative, not file-relative: the indexer parses
// v.ReadBody (frontmatter stripped) and ToggleInFile strips it the same way, so
// a line from query_tasks addresses the same task here. If the two ever
// disagreed, completing a task in any note with frontmatter would silently tick
// a different one — this pins the contract.
func TestToggleInFileLinesAreBodyRelative(t *testing.T) {
	root := t.TempDir()
	rel := "note.md"
	original := "---\ntitle: Example\ntags: [a]\n---\n\n- [ ] First\n- [ ] Second\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Body is "\n- [ ] First\n- [ ] Second\n" — so "Second" is body line 3,
	// which is file line 7. Addressing it by the file line would hit nothing.
	if _, err := ToggleInFile(root, rel, 3, FileVersion([]byte(original)), true, time.Now()); err != nil {
		t.Fatalf("toggle: %v", err)
	}

	out, _ := os.ReadFile(filepath.Join(root, rel))
	got := string(out)
	if !strings.HasPrefix(got, "---\ntitle: Example\ntags: [a]\n---\n") {
		t.Errorf("frontmatter was altered:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] First") {
		t.Errorf("wrong task toggled:\n%s", got)
	}
	if !strings.Contains(got, "- [x] Second") {
		t.Errorf("target task not completed:\n%s", got)
	}
}

func TestToggleInFileRejectsNonTaskLine(t *testing.T) {
	root := t.TempDir()
	rel := "note.md"
	if err := os.WriteFile(filepath.Join(root, rel),
		[]byte("just prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, rel))

	if _, err := ToggleInFile(root, rel, 1, FileVersion(before), true, time.Now()); err == nil {
		t.Fatal("expected an error for a non-task line")
	}
	after, _ := os.ReadFile(filepath.Join(root, rel))
	if string(before) != string(after) {
		t.Error("file must be left untouched when the toggle is rejected")
	}
}
