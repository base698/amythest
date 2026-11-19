// Package termimg draws images in a terminal: Kitty graphics with Unicode
// placeholders where the terminal supports them, coloured half blocks
// anywhere else. The wall and the Messages client share it.
package termimg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strings"
)

// Decode reads a PNG, JPEG or GIF (first frame). Other formats are an error
// the feed shows as a plain row.
func Decode(b []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

// BoxFor sizes an image to a block of terminal cells: as wide as maxCols
// allows, no taller than maxRows, keeping its proportions. cellAspect is a
// cell's width divided by its height (about 0.5).
func BoxFor(imgW, imgH, maxCols, maxRows int, cellAspect float64) (cols, rows int) {
	if imgW <= 0 || imgH <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	if cellAspect <= 0 {
		cellAspect = 0.5
	}
	cols = maxCols
	rows = int(float64(cols)*cellAspect*float64(imgH)/float64(imgW) + 0.5)
	if rows > maxRows {
		rows = maxRows
		cols = int(float64(rows)*float64(imgW)/(float64(imgH)*cellAspect) + 0.5)
	}
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// Shrink scales an image down to w×h by averaging the source pixels under
// each destination pixel, which keeps downsized screenshots readable.
func Shrink(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
		b = rgba.Bounds()
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, (y+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint32
			for yy := y0; yy < y1 && yy < sh; yy++ {
				row := rgba.Pix[(yy+b.Min.Y-rgba.Rect.Min.Y)*rgba.Stride:]
				for xx := x0; xx < x1 && xx < sw; xx++ {
					p := row[(xx+b.Min.X-rgba.Rect.Min.X)*4:]
					r, g, bl, a, n = r+uint32(p[0]), g+uint32(p[1]), bl+uint32(p[2]), a+uint32(p[3]), n+1
				}
			}
			if n > 0 {
				dst.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), uint8(a / n)})
			}
		}
	}
	return dst
}

// --- Kitty graphics, Unicode placeholders ---------------------------------
//
// The image is sent to the terminal once, tagged with an id, as a "virtual
// placement". It is then drawn wherever the text contains Placeholder cells:
// U+10EEEE coloured with the id, each carrying two combining marks that give
// its row and column. Because the cells are ordinary text, the image scrolls,
// clips and redraws with the rest of the screen.

const Placeholder = '\U0010EEEE'

// MaxCells is how many rows or columns a Placeholder block can address.
const MaxCells = len(Diacritics)

// KittyTransmit returns the escape sequence that uploads a PNG as image id
// with a virtual placement of cols×rows cells.
func KittyTransmit(id, cols, rows int, pngData []byte) string {
	enc := base64.StdEncoding.EncodeToString(pngData)
	var b strings.Builder
	first := true
	for len(enc) > 0 {
		n := min(4096, len(enc))
		chunk := enc[:n]
		enc = enc[n:]
		more := 0
		if len(enc) > 0 {
			more = 1
		}
		if first {
			// q=2: the terminal must not answer; a reply would arrive as keystrokes.
			fmt.Fprintf(&b, "\x1b_Ga=T,U=1,q=2,f=100,i=%d,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, chunk)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return b.String()
}

// KittyDelete frees an uploaded image.
func KittyDelete(id int) string { return fmt.Sprintf("\x1b_Ga=d,d=I,q=2,i=%d\x1b\\", id) }

// KittyRows returns the text rows that display image id over cols×rows cells.
func KittyRows(id, cols, rows int) []string {
	cols, rows = min(cols, MaxCells), min(rows, MaxCells)
	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", id>>16&255, id>>8&255, id&255)
		for c := 0; c < cols; c++ {
			b.WriteRune(Placeholder)
			b.WriteRune(Diacritics[r])
			b.WriteRune(Diacritics[c])
		}
		b.WriteString("\x1b[39m")
		out[r] = b.String()
	}
	return out
}

// --- half blocks: the fallback every colour terminal can draw --------------

// BlockRows draws an image as cols×rows cells of "▀", the upper pixel as the
// foreground colour and the lower as the background: two pixels per cell.
func BlockRows(src image.Image, cols, rows int) []string {
	img := Shrink(src, cols, rows*2)
	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			top, bot := img.RGBAAt(c, r*2), img.RGBAAt(c, r*2+1)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.R, top.G, top.B, bot.R, bot.G, bot.B)
		}
		b.WriteString("\x1b[0m")
		out[r] = b.String()
	}
	return out
}

func EncodePNG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
