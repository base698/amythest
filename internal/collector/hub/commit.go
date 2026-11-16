package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/base698/amythest/internal/collector/collect"
	"os"
	"path/filepath"
)

func (q *Queue) stageCommit(a Artifact) (string, error) {
	patch, e := collect.CommitPatch(a.Repo, a.Ref)
	if e != nil {
		return "", e
	}
	dir, e := q.mediaRoom(int64(len(patch)))
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(patch)
	digest := hex.EncodeToString(sum[:])
	f, e := os.CreateTemp(dir, ".commit-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(patch); e != nil {
		return "", e
	}
	if e = f.Sync(); e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(f.Name(), filepath.Join(dir, digest)); e != nil {
		return "", e
	}
	d, e := os.Open(dir)
	if e != nil {
		return "", e
	}
	defer d.Close()
	return digest, d.Sync()
}
