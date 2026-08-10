package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The amethyst. Pure ASCII so terminal width math stays honest; the shimmer
// comes from sweeping a purple gradient diagonally across the facets, with a
// white sparkle that orbits the outline.
var gemArt = []string{
	`    ______    `,
	`   /\    /\   `,
	`  /  \  /  \  `,
	` /____\/____\ `,
	` \    /\    / `,
	`  \  /  \  /  `,
	`   \/____\/   `,
	`    \    /    `,
	`     \  /     `,
	`      \/      `,
}

const gemCaption = "amythest"

// Purple-to-lavender sweep; index by (col+row+phase) so the band moves
// diagonally, which reads as light passing over the stone.
var gemPalette = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("54")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("55")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("92")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("93")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("129")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("135")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("177")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("183")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("219")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("183")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("135")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("93")),
}

var sparkleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))

// renderGem draws the gem for one shimmer phase. Non-structural characters
// (spaces) pass through untouched so alignment can't drift.
func renderGem(phase int) string {
	var b strings.Builder
	// Count drawable cells so the sparkle can orbit them evenly.
	drawable := 0
	for _, line := range gemArt {
		for _, r := range line {
			if r != ' ' {
				drawable++
			}
		}
	}
	sparkleAt := -1
	if drawable > 0 {
		sparkleAt = (phase * 3) % drawable
	}
	cell := 0
	for row, line := range gemArt {
		for col, r := range line {
			if r == ' ' {
				b.WriteRune(' ')
				continue
			}
			if cell == sparkleAt {
				b.WriteString(sparkleStyle.Render(string(r)))
			} else {
				style := gemPalette[(col+row+phase)%len(gemPalette)]
				b.WriteString(style.Render(string(r)))
			}
			cell++
		}
		b.WriteRune('\n')
	}
	caption := gemPalette[phase%len(gemPalette)].Render(gemCaption)
	pad := (len(gemArt[0]) - len(gemCaption)) / 2
	if pad > 0 {
		caption = strings.Repeat(" ", pad) + caption
	}
	b.WriteString(caption)
	return b.String()
}

func gemWidth() int  { return len(gemArt[0]) }
func gemHeight() int { return len(gemArt) + 1 }
