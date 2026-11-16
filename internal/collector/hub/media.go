package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const mediaFileLimit = 50 << 20
const mediaOutboxLimit = 512 << 20

// Stage an immutable copy before publishing its hash. Discovery paths can change
// or disappear after a turn; retries must upload the exact bytes originally seen.
// Caller holds mediaMu through the queue update that records the pending upload.
func (q *Queue) stageMedia(a Artifact) (string, error) {
	if a.MediaHash != "" || (a.Kind != "image" && a.Kind != "video") || !filepath.IsAbs(a.RemotePath) {
		return a.MediaHash, nil
	}
	info, e := os.Lstat(a.RemotePath)
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Size() > mediaFileLimit {
		return "", errors.New("media is not a regular file within the 50 MiB limit")
	}
	src, e := os.Open(a.RemotePath)
	if e != nil {
		return "", e
	}
	defer src.Close()
	opened, e := src.Stat()
	if e != nil {
		return "", e
	}
	if !os.SameFile(info, opened) {
		return "", errors.New("media file changed while opening")
	}
	header := make([]byte, 512)
	n, _ := src.Read(header)
	content := http.DetectContentType(header[:n])
	allowed := map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "video/mp4": true, "video/webm": true}
	if !allowed[content] || !strings.HasPrefix(content, a.Kind+"/") {
		return "", errors.New("unsupported media content")
	}
	if _, e = src.Seek(0, 0); e != nil {
		return "", e
	}
	dir := filepath.Join(filepath.Dir(q.path), "media-outbox")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	files, e := os.ReadDir(dir)
	if e != nil {
		return "", e
	}
	var total int64
	for _, f := range files {
		if i, e := f.Info(); e == nil {
			total += i.Size()
		}
	}
	if total+info.Size() > mediaOutboxLimit {
		return "", errors.New("media outbox is full")
	}
	dst, e := os.CreateTemp(dir, ".capture-")
	if e != nil {
		return "", e
	}
	defer os.Remove(dst.Name())
	defer dst.Close()
	hash := sha256.New()
	size, e := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(src, mediaFileLimit+1))
	if e != nil {
		return "", e
	}
	if size > mediaFileLimit {
		return "", errors.New("media grew beyond the file limit")
	}
	after, e := src.Stat()
	if e != nil {
		return "", e
	}
	if size != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", errors.New("media changed during capture")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if e = dst.Sync(); e != nil {
		return "", e
	}
	if e = dst.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(dst.Name(), filepath.Join(dir, digest)); e != nil {
		return "", e
	}
	directory, e := os.Open(dir)
	if e != nil {
		return "", e
	}
	defer directory.Close()
	return digest, directory.Sync()
}

func mediaDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Media retries are separate from event acknowledgement. A failed upload never
// prevents lifecycle facts and bindings from reaching the server on the next sync.
func (c *Client) syncMedia(q *Queue) error {
	q.mediaMu.Lock()
	defer q.mediaMu.Unlock()
	var failures []error
	count := 0
	for digest := range q.Snapshot().MediaPending {
		if count >= 1 {
			break
		}
		count++
		if !mediaDigest(digest) {
			failures = append(failures, errors.New("invalid media outbox hash"))
			continue
		}
		path := filepath.Join(filepath.Dir(q.path), "media-outbox", digest)
		if e := c.uploadMedia(path, digest); e != nil {
			failures = append(failures, e)
			continue
		}
		if e := q.Update(func(s *State) error { delete(s.MediaPending, digest); return nil }); e != nil {
			return e
		}
		if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}
func (c *Client) uploadMedia(path, digest string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	req, e := http.NewRequest("PUT", c.Credential.Server+"/api/work/media/"+digest, f)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.Credential.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	uploadClient := *c.HTTP
	uploadClient.Timeout = time.Minute
	res, e := uploadClient.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("media upload: HTTP %d; file retained for retry", res.StatusCode)
	}
	return nil
}
