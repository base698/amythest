package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Experimental gem intro animation via ttfx (github.com/omacom/ttfx), the
// Rust TerminalTextEffects port. Opt-in: set AMY_GEM_FX to an effect name
// plus optional effect flags, e.g.
//
//	AMY_GEM_FX="decrypt"
//	AMY_GEM_FX="beams --final-gradient-stops 8A2BE2 D787FF"
//
// The binary is found on PATH or via AMY_TTFX. Frames come from ttfx's
// --parity-dump mode: length-prefixed, cursor-escape-free canvas blocks that
// drop straight into the gem's slot. When the effect finishes, the built-in
// shimmer takes over.

const (
	gemFXMaxFrames = 1200
	gemFXMaxBytes  = 8 << 20
)

type gemFXMsg struct {
	frames []string
	err    error
}

// gemFXSpec returns the ttfx invocation from the environment, or ok=false.
func gemFXSpec() (binary string, args []string, ok bool) {
	spec := strings.Fields(os.Getenv("AMY_GEM_FX"))
	if len(spec) == 0 {
		return "", nil, false
	}
	binary = os.Getenv("AMY_TTFX")
	if binary == "" {
		path, err := exec.LookPath("ttfx")
		if err != nil {
			return "", nil, false
		}
		binary = path
	}
	return binary, spec, true
}

// loadGemFXCmd runs ttfx over the gem art and parses the frame stream.
func loadGemFXCmd(binary string, effectArgs []string) tea.Cmd {
	art := strings.Join(gemArt, "\n") + "\n" + centeredGemCaption() + "\n"
	return func() tea.Msg {
		args := append([]string{"--parity-dump", "--seed", "42",
			"--max-frames", strconv.Itoa(gemFXMaxFrames)}, effectArgs...)
		cmd := exec.Command(binary, args...)
		cmd.Stdin = strings.NewReader(art)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return gemFXMsg{err: fmt.Errorf("ttfx: %w", err)}
		}
		frames, err := parseTTFXFrames(out.Bytes())
		if err != nil {
			return gemFXMsg{err: err}
		}
		return gemFXMsg{frames: frames}
	}
}

// parseTTFXFrames decodes ttfx --parity-dump output: repeated
// "<len>\n<len bytes>\n" records, each a full canvas frame.
func parseTTFXFrames(raw []byte) ([]string, error) {
	if len(raw) > gemFXMaxBytes {
		return nil, fmt.Errorf("ttfx output too large (%d bytes)", len(raw))
	}
	var frames []string
	rest := raw
	for len(rest) > 0 {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		length, err := strconv.Atoi(strings.TrimSpace(string(rest[:nl])))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("bad frame header %q", rest[:min(nl, 20)])
		}
		rest = rest[nl+1:]
		if length > len(rest) {
			return nil, fmt.Errorf("truncated frame: want %d bytes, have %d", length, len(rest))
		}
		frames = append(frames, string(rest[:length]))
		rest = rest[length:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
		if len(frames) >= gemFXMaxFrames {
			break
		}
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("ttfx produced no frames")
	}
	return frames, nil
}

func centeredGemCaption() string {
	pad := (len(gemArt[0]) - len(gemCaption)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + gemCaption
}
