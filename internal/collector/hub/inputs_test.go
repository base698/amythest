package hub

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSharedInputsOptInReplayAndPrivacy(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "lifecycle-only", true: "inputs"}[capture], func(t *testing.T) {
			dir := t.TempDir()
			q, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			path, config := filepath.Join(dir, "rollout.jsonl"), filepath.Join(dir, "codex.json")
			var content bytes.Buffer
			content.WriteString(`{"type":"session_meta","payload":{"id":"chat"}}` + "\n")
			content.WriteString(`{"timestamp":"2026-11-17T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn"}}` + "\n")
			record := func(role string, blocks ...map[string]string) {
				b, err := json.Marshal(map[string]any{"timestamp": "2026-11-17T12:01:00Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": blocks}})
				if err != nil {
					t.Fatal(err)
				}
				content.Write(b)
				content.WriteByte('\n')
			}
			record("user", map[string]string{"type": "input_text", "text": "<environment_context>PRIVATE CONTEXT</environment_context>"})
			record("user", map[string]string{"type": "input_text", "text": "# AGENTS.md instructions PRIVATE RULES"})
			record("assistant", map[string]string{"type": "text", "text": "PRIVATE RESPONSE"})
			record("tool", map[string]string{"type": "text", "text": "PRIVATE TOOL"})
			record("user", map[string]string{"type": "input_text", "text": `<image name="Screenshot" path="/not/a/readable/path/screenshot.png">`},
				map[string]string{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(samplePNG(t))},
				map[string]string{"type": "input_text", "text": "</image>"}, map[string]string{"type": "input_text", "text": "Show this screenshot under Inputs"})
			content.WriteString(`{"timestamp":"2026-11-17T12:05:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn"}}` + "\n")
			os.WriteFile(path, content.Bytes(), 0600)
			b, _ := json.Marshal([]CodexWatch{{Session: "chat", Path: path, Project: "robot", CaptureInputs: capture}})
			os.WriteFile(config, b, 0600)
			if err = q.PollCodex(config); err != nil {
				t.Fatal(err)
			}
			state := q.Snapshot()
			messages, attachments := 0, 0
			for _, ev := range state.Events {
				if ev.Kind == "chat" {
					messages++
					if ev.Role != "user" || ev.Text != "Show this screenshot under Inputs" || ev.Run == "" || ev.DocumentID == "" {
						t.Fatal(ev)
					}
				}
				if ev.Kind == "artifact" {
					attachments++
					a := ev.Artifact
					if !strings.HasPrefix(a.Key, "input:") || a.Title != "screenshot.png" || a.RemotePath != "" || ev.Run == "" || !state.MediaPending[a.MediaHash] {
						t.Fatal(ev)
					}
					staged, err := os.ReadFile(filepath.Join(dir, "media-outbox", a.MediaHash))
					if err != nil || !bytes.Equal(staged, samplePNG(t)) {
						t.Fatal("attachment bytes changed", err)
					}
				}
			}
			want := 0
			if capture {
				want = 1
			}
			if messages != want || attachments != want {
				t.Fatalf("messages=%d attachments=%d", messages, attachments)
			}
			saved, _ := os.ReadFile(filepath.Join(dir, "hub.json"))
			if strings.Contains(string(saved), "PRIVATE") || strings.Contains(string(saved), "not/a/readable") {
				t.Fatal("captured private context or source path")
			}
			q.Close()
			q, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			q.Update(func(s *State) error { s.CodexOffsets = nil; return nil })
			if err = q.PollCodex(config); err != nil || len(q.Snapshot().Events) != len(state.Events) {
				t.Fatal("replay duplicated input", err)
			}
		})
	}
}

func TestInputImageRejectsURLsDisguisedBytesAndWrongType(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, value := range []string{"https://example.test/image.png", "file:///private/input.png", "data:image/png;base64,broken", "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("private text")), "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(samplePNG(t))} {
		if _, err = q.stageInputImage(value); err == nil {
			t.Fatal("accepted unsafe or invalid image")
		}
	}
}
