package hub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var inputImageName = regexp.MustCompile(`<image\s+[^>]*path="([^"]+)"`)
var errInputImage = errors.New("unsupported or invalid input image")

func (q *Queue) codexInput(w CodexWatch, record codexRecord, line []byte) error {
	digest := sha256.Sum256(line)
	receipt := "input:" + hookKey(w.Session, hex.EncodeToString(digest[:]))
	state := q.Snapshot()
	if state.HookEvents[receipt] {
		return nil
	}
	active := false
	for _, session := range state.Sessions {
		if session.Pane.Agent == "codex" && session.Pane.Provider == w.Session && session.Run != "" {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	var blocks []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	if json.Unmarshal(record.Payload.Content, &blocks) != nil {
		return nil
	}
	var texts []string
	var images []Artifact
	q.mediaMu.Lock()
	defer q.mediaMu.Unlock()
	imageName := "Shared image"
	for _, b := range blocks {
		switch b.Type {
		case "input_text", "text":
			text := strings.TrimSpace(b.Text)
			if match := inputImageName.FindStringSubmatch(text); strings.HasPrefix(text, "<image ") && len(match) > 1 {
				imageName = filepath.Base(match[1])
				if len(imageName) > 1000 {
					imageName = "Shared image"
				}
				continue
			}
			if text == "</image>" {
				continue
			}
			// These user-role wrappers are app/environment context, not shared inputs.
			if strings.HasPrefix(text, "<environment_context>") || strings.HasPrefix(text, "<user_instructions>") || strings.HasPrefix(text, "# AGENTS.md instructions") {
				continue
			}
			if text != "" {
				texts = append(texts, text)
			}
		case "input_image":
			if !strings.HasPrefix(b.ImageURL, "data:image/") {
				continue
			}
			hash, err := q.stageInputImage(b.ImageURL)
			if errors.Is(err, errInputImage) {
				texts = append(texts, "[Image attachment could not be captured]")
				continue
			}
			if err != nil {
				return err
			}
			key := fmt.Sprintf("%s:image:%d", receipt, len(images))
			images = append(images, Artifact{Key: key, Kind: "image", Ref: key, Title: imageName, MediaHash: hash, Attribution: "exact"})
			imageName = "Shared image"
		}
	}
	text := strings.Join(texts, "\n\n")
	if len(text) > 65500 {
		text = strings.ToValidUTF8(text[:65500], "") + "\n[Input truncated]"
	}
	if text == "" && len(images) == 0 {
		return nil
	}
	return q.Update(func(s *State) error {
		if s.HookEvents[receipt] {
			return nil
		}
		var session Session
		for _, p := range s.Sessions {
			if p.Pane.Agent == "codex" && p.Pane.Provider == w.Session {
				session = p
				break
			}
		}
		if session.Run == "" {
			return nil
		}
		if text != "" {
			ev := s.event(session, "chat", record.Timestamp)
			ev.DocumentID, ev.Role, ev.Text = receipt, "user", text
			s.append(ev)
		}
		for _, a := range images {
			ev := s.event(session, "artifact", record.Timestamp)
			ev.Artifact = &a
			s.append(ev)
			if s.MediaPending == nil {
				s.MediaPending = map[string]bool{}
			}
			s.MediaPending[a.MediaHash] = true
		}
		if s.HookEvents == nil {
			s.HookEvents = map[string]bool{}
		}
		s.HookEvents[receipt] = true
		return nil
	})
}

// Only bytes embedded as an attachment are accepted. Never follow a URL or
// read arbitrary paths found in prompt text.
func (q *Queue) stageInputImage(dataURL string) (string, error) {
	header, encoded, ok := strings.Cut(dataURL, ",")
	allowed := map[string]bool{"data:image/png;base64": true, "data:image/jpeg;base64": true, "data:image/webp;base64": true, "data:image/gif;base64": true}
	if !ok || !allowed[header] || len(encoded) > base64.StdEncoding.EncodedLen(mediaFileLimit) {
		return "", errInputImage
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) > mediaFileLimit {
		return "", errInputImage
	}
	if "data:"+http.DetectContentType(data)+";base64" != header {
		return "", errInputImage
	}
	dir, err := q.mediaRoom(int64(len(data)))
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	f, err := os.CreateTemp(dir, ".input-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, digest)); err != nil {
		return "", err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	return digest, directory.Sync()
}
