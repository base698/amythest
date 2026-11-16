package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func samplePNG(t *testing.T) []byte {
	t.Helper()
	b, e := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jipkAAAAASUVORK5CYII=")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestMediaOutboxSurvivesAckRestartAndChangedSource(t *testing.T) {
	dir := t.TempDir()
	q, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(dir, "result.png")
	original := samplePNG(t)
	if e = os.WriteFile(source, original, 0600); e != nil {
		t.Fatal(e)
	}
	job := Job{Session: Session{ID: "session", Run: "run"}, At: time.Now()}
	if e = q.Update(func(s *State) error { s.Jobs = []Job{job}; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = q.Complete(job, []Artifact{{Key: "image", Kind: "image", Ref: source, RemotePath: source}}); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(original)
	digest := hex.EncodeToString(hash[:])
	state := q.Snapshot()
	if len(state.Events) != 1 || state.Events[0].Artifact.MediaHash != digest || !state.MediaPending[digest] {
		t.Fatal(state)
	}
	if e = q.Ack(state.Generation, state.Next-1); e != nil {
		t.Fatal(e)
	}
	q.Close()
	q, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	os.WriteFile(source, []byte("changed after capture"), 0600)
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/api/work/media/"+digest || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("incorrect upload request")
		}
		b, _ := io.ReadAll(r.Body)
		if !bytes.Equal(b, original) {
			t.Error("uploaded a later file version")
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := Client{Credential: Credential{Server: server.URL, Token: "test-token"}, HTTP: server.Client()}
	if e = client.syncMedia(q); e == nil {
		t.Fatal("failed upload reported success")
	}
	if !q.Snapshot().MediaPending[digest] {
		t.Fatal("lost failed upload")
	}
	fail.Store(false)
	if e = client.syncMedia(q); e != nil {
		t.Fatal(e)
	}
	if len(q.Snapshot().MediaPending) != 0 || len(q.Snapshot().Events) != 0 {
		t.Fatal("upload was not acknowledged independently")
	}
	if _, e = os.Stat(filepath.Join(dir, "media-outbox", digest)); !os.IsNotExist(e) {
		t.Fatal("delivered snapshot not cleaned up", e)
	}
}
func TestMediaStagingRejectsSymlinksAndDisguisedFiles(t *testing.T) {
	dir := t.TempDir()
	q, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	source := filepath.Join(dir, "source.png")
	os.WriteFile(source, samplePNG(t), 0600)
	link := filepath.Join(dir, "link.png")
	os.Symlink(source, link)
	if _, e = q.stageMedia(Artifact{Kind: "image", RemotePath: link}); e == nil {
		t.Fatal("followed symlink")
	}
	os.WriteFile(source, []byte("not an image"), 0600)
	if _, e = q.stageMedia(Artifact{Kind: "image", RemotePath: source}); e == nil {
		t.Fatal("uploaded disguised text")
	}
	os.WriteFile(source, samplePNG(t), 0600)
	f, e := os.OpenFile(source, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.Truncate(mediaFileLimit + 1)
	f.Close()
	if _, e = q.stageMedia(Artifact{Kind: "image", RemotePath: source}); e == nil {
		t.Fatal("staged oversized file")
	}
}

func TestCompletedCommitQueuesCapturedDiff(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.Mkdir(repo, 0700)
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Example", "GIT_AUTHOR_EMAIL=example@example.com", "GIT_COMMITTER_NAME=Example", "GIT_COMMITTER_EMAIL=example@example.com")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatal(string(b), e)
		}
		return strings.TrimSpace(string(b))
	}
	git("init")
	os.WriteFile(filepath.Join(repo, "sample.txt"), []byte("captured version\n"), 0600)
	git("add", ".")
	git("commit", "-m", "Capture a change")
	sha := git("rev-parse", "HEAD")
	q, e := Open(filepath.Join(dir, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	job := Job{Session: Session{ID: "session", Run: "run"}, At: time.Now()}
	if e = q.Update(func(s *State) error { s.Jobs = []Job{job}; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = q.Complete(job, []Artifact{{Key: "commit:" + sha, Kind: "commit", Ref: sha, Repo: repo}}); e != nil {
		t.Fatal(e)
	}
	state := q.Snapshot()
	a := state.Events[0].Artifact
	if a.MediaHash == "" || !state.MediaPending[a.MediaHash] {
		t.Fatal("commit has no durable upload", state)
	}
	patch, e := os.ReadFile(filepath.Join(dir, "state", "media-outbox", a.MediaHash))
	if e != nil || !bytes.Contains(patch, []byte(sha)) || !bytes.Contains(patch, []byte("+captured version")) {
		t.Fatal(string(patch), e)
	}
}
