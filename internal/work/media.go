package work

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type Media struct {
	Hash  string `json:"hash"`
	Bytes int64  `json:"bytes"`
	Type  string `json:"content_type"`
}

func (s *Store) Upload(collector, digest string, src io.Reader) (Media, error) {
	m := Media{Hash: digest}
	if !validHash(digest) {
		return m, invalid("SHA-256 required")
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	f, e := os.CreateTemp(s.cfg.MediaDir, ".upload-")
	if e != nil {
		return m, e
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	h := sha256.New()
	m.Bytes, e = io.Copy(io.MultiWriter(f, h), io.LimitReader(src, s.cfg.MediaMaxFile+1))
	if e != nil {
		return m, e
	}
	if m.Bytes > s.cfg.MediaMaxFile {
		return m, &Fault{Status: 413, Message: "file exceeds media limit"}
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return m, invalid("media hash mismatch")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return m, e
	}
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	m.Type = http.DetectContentType(buf[:n])
	switch m.Type {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "video/mp4", "video/webm":
	default:
		m.Type = "application/octet-stream"
	}
	if e = f.Sync(); e != nil {
		return m, e
	}
	if e = f.Close(); e != nil {
		return m, e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	var total, existing int64
	if e = tx.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM media WHERE available=1`).Scan(&total); e != nil {
		return m, e
	}
	if e = tx.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM media WHERE hash=? AND available=1`, digest).Scan(&existing); e != nil {
		return m, e
	}

	victims := []string{}
	if total-existing+m.Bytes > s.cfg.MediaMaxTotal {
		rows, err := tx.Query(`SELECT hash,bytes FROM media m WHERE available=1 AND hash!=? AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN links l ON l.artifact_id=a.id WHERE a.media_hash=m.hash AND l.featured=1) ORDER BY created,hash`, digest)
		if err != nil {
			return m, err
		}
		for rows.Next() {
			var key string
			var size int64
			if err = rows.Scan(&key, &size); err != nil {
				rows.Close()
				return m, err
			}
			victims = append(victims, key)
			total -= size
			if total-existing+m.Bytes <= s.cfg.MediaMaxTotal {
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return m, err
		}
		if total-existing+m.Bytes > s.cfg.MediaMaxTotal {
			return m, &Fault{Status: 507, Message: "media quota is protected by featured output"}
		}
		for _, v := range victims {
			if _, e = tx.Exec(`UPDATE media SET available=0 WHERE hash=?`, v); e != nil {
				return m, e
			}
		}
	}

	if e = os.Rename(name, filepath.Join(s.cfg.MediaDir, digest)); e != nil {
		return m, e
	}
	dir, e := os.Open(s.cfg.MediaDir)
	if e != nil {
		return m, e
	}
	e = dir.Sync()
	dir.Close()
	if e != nil {
		return m, e
	}
	_, e = tx.Exec(`INSERT INTO media VALUES(?,?,?,?,1) ON CONFLICT(hash) DO UPDATE SET available=1`, digest, m.Bytes, m.Type, nowText(s.now()))
	if e == nil {
		_, e = tx.Exec(`INSERT INTO media_owners VALUES(?,?) ON CONFLICT DO NOTHING`, digest, collector)
	}
	if e != nil {
		return m, e
	}
	if e = tx.Commit(); e != nil {
		return m, e
	}
	for _, v := range victims {
		_ = os.Remove(filepath.Join(s.cfg.MediaDir, v))
	}
	return m, nil
}
func (s *Store) OpenMedia(digest string) (*os.File, Media, error) {
	m := Media{Hash: digest}
	if !validHash(digest) {
		return nil, m, os.ErrNotExist
	}
	e := s.db.QueryRow(`SELECT bytes,content_type FROM media WHERE hash=? AND available=1`, digest).Scan(&m.Bytes, &m.Type)
	if e != nil {
		return nil, m, os.ErrNotExist
	}
	f, e := os.Open(filepath.Join(s.cfg.MediaDir, digest))
	return f, m, e
}

// Evict removes only blobs with no featured association. Metadata and provenance
// survive so the UI can explain that a file needs uploading again.
func (s *Store) Evict(digest string) error {
	if !validHash(digest) {
		return invalid("invalid hash")
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	res, e := s.db.Exec(`UPDATE media SET available=0 WHERE hash=? AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN links l ON l.artifact_id=a.id WHERE a.media_hash=? AND l.featured=1)`, digest, digest)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return conflict("media missing or featured")
	}
	e = os.Remove(filepath.Join(s.cfg.MediaDir, digest))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}

// Only a blob explicitly attached to a commit may be presented as a text diff.
func (s *Store) IsCommitMedia(digest string) (bool, error) {
	var exists bool
	e := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM artifacts WHERE kind='commit' AND media_hash=?)`, digest).Scan(&exists)
	return exists, e
}
