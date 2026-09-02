package tui

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Gem intro animation, rendered with ttfx (github.com/omacom/ttfx — the
// Rust port of ChrisBuilds' TerminalTextEffects; both MIT).
//
// By default amy plays an embedded, pre-rendered purple "beams" pass over
// the gem (frames baked at build time from ttfx --parity-dump: length-
// prefixed, cursor-escape-free canvas blocks). When the pass finishes, the
// built-in shimmer takes over and the amethyst stays put.
//
// Overrides via AMY_GEM_FX:
//
//	AMY_GEM_FX=off                    # no intro, straight to the shimmer
//	AMY_GEM_FX="decrypt"              # any live ttfx effect (needs the
//	AMY_GEM_FX="fireworks --…flags"   # binary on PATH, or AMY_TTFX=/path)
//
// Regenerate the embedded frames with `mise run gemfx` (needs ttfx).

const (
	gemFXMaxFrames = 1200
	gemFXMaxBytes  = 8 << 20
)

type gemFXMsg struct {
	frames []string
	err    error
}

//go:embed fxdata/beams.dump.gz
var embeddedBeams []byte

var (
	embeddedFramesOnce sync.Once
	embeddedFrames     []string
)

// gemFXCmd picks the intro source: AMY_GEM_FX=off disables it, a set value
// runs live ttfx, and the default is the embedded beams pass.
func gemFXCmd() tea.Cmd {
	spec := strings.Fields(os.Getenv("AMY_GEM_FX"))
	if len(spec) == 1 && strings.EqualFold(spec[0], "off") {
		return nil
	}
	if len(spec) > 0 {
		binary := os.Getenv("AMY_TTFX")
		if binary == "" {
			path, err := exec.LookPath("ttfx")
			if err != nil {
				return nil // requested a live effect but no binary: shimmer only
			}
			binary = path
		}
		return loadGemFXCmd(binary, spec)
	}
	return func() tea.Msg {
		frames, err := embeddedGemFrames()
		if err != nil {
			return gemFXMsg{err: err}
		}
		return gemFXMsg{frames: frames}
	}
}

// embeddedGemFrames decompresses and parses the baked beams pass once.
func embeddedGemFrames() ([]string, error) {
	var err error
	embeddedFramesOnce.Do(func() {
		zr, zerr := gzip.NewReader(bytes.NewReader(embeddedBeams))
		if zerr != nil {
			err = zerr
			return
		}
		raw, rerr := io.ReadAll(io.LimitReader(zr, gemFXMaxBytes))
		if rerr != nil {
			err = rerr
			return
		}
		embeddedFrames, err = parseTTFXFrames(raw)
	})
	if err != nil {
		return nil, fmt.Errorf("embedded gem frames: %w", err)
	}
	return embeddedFrames, nil
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
