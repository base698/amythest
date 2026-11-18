package canvas

// Native edits save scene geometry without opening Excalidraw. Render a clean,
// bounded preview of that geometry until the browser supplies its own PNG.
import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font/gofont/goregular"
	_ "golang.org/x/image/webp"
)

type previewElement struct {
	rawElement
	Angle       float64         `json:"angle"`
	Opacity     *float64        `json:"opacity"`
	StrokeWidth float64         `json:"strokeWidth"`
	StrokeStyle string          `json:"strokeStyle"`
	FontSize    float64         `json:"fontSize"`
	TextAlign   string          `json:"textAlign"`
	LineHeight  float64         `json:"lineHeight"`
	Roundness   json.RawMessage `json:"roundness"`
}

type previewScene struct {
	Elements []previewElement `json:"elements"`
	Files    map[string]struct {
		DataURL string `json:"dataURL"`
	} `json:"files"`
}

var previewFont, _ = truetype.Parse(goregular.TTF)

func previewPNG(raw []byte) ([]byte, error) {
	var scene previewScene
	if err := json.Unmarshal(raw, &scene); err != nil {
		return nil, err
	}
	if len(scene.Elements) > 10000 {
		return nil, fmt.Errorf("canvas too large to preview")
	}
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	points := 0
	for _, e := range scene.Elements {
		if e.IsDeleted {
			continue
		}
		for _, n := range []float64{e.X, e.Y, e.Width, e.Height, e.Angle, e.StrokeWidth, e.FontSize} {
			if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e7 {
				return nil, fmt.Errorf("invalid canvas geometry")
			}
		}
		pts := [][2]float64{{e.X, e.Y}, {e.X + e.Width, e.Y}, {e.X + e.Width, e.Y + e.Height}, {e.X, e.Y + e.Height}}
		if len(e.Points) > 0 {
			points += len(e.Points)
			if points > 200000 {
				return nil, fmt.Errorf("canvas has too many points to preview")
			}
			pts = nil
			for _, p := range e.Points {
				if len(p) != 2 || math.Abs(p[0]) > 1e7 || math.Abs(p[1]) > 1e7 {
					return nil, fmt.Errorf("invalid canvas point")
				}
				pts = append(pts, [2]float64{e.X + p[0], e.Y + p[1]})
			}
		}
		cx, cy := e.X+e.Width/2, e.Y+e.Height/2
		c, s := math.Cos(e.Angle), math.Sin(e.Angle)
		for _, p := range pts {
			x, y := cx+(p[0]-cx)*c-(p[1]-cy)*s, cy+(p[0]-cx)*s+(p[1]-cy)*c
			x0, y0, x1, y1 = math.Min(x0, x), math.Min(y0, y), math.Max(x1, x), math.Max(y1, y)
		}
	}
	if math.IsInf(x0, 1) {
		x0, y0, x1, y1 = 0, 0, 1, 1
	}
	const pad = 24.0
	w, h := x1-x0+2*pad, y1-y0+2*pad
	scale := math.Min(2, 1600/math.Max(w, h))
	dc := gg.NewContext(max(1, int(math.Ceil(w*scale))), max(1, int(math.Ceil(h*scale))))
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.Scale(scale, scale)
	dc.Translate(pad-x0, pad-y0)
	for _, e := range scene.Elements {
		if e.IsDeleted {
			continue
		}
		opacity := 100.0
		if e.Opacity != nil {
			opacity = math.Max(0, math.Min(100, *e.Opacity))
		}
		if opacity == 0 {
			continue
		}
		dc.Push()
		dc.RotateAbout(e.Angle, e.X+e.Width/2, e.Y+e.Height/2)
		stroke := e.StrokeColor
		if stroke == "" {
			stroke = "#1e1e1e"
		}
		dc.SetColor(previewColor(stroke, opacity))
		dc.SetLineWidth(math.Max(1/scale, math.Min(100, math.Max(1, e.StrokeWidth))))
		dc.SetLineCapRound()
		dc.SetLineJoinRound()
		switch e.StrokeStyle {
		case "dashed":
			dc.SetDash(8, 6)
		case "dotted":
			dc.SetDash(1.5, 5)
		}
		switch e.Type {
		case "rectangle", "frame", "magicframe", "ellipse", "diamond":
			switch e.Type {
			case "ellipse":
				dc.DrawEllipse(e.X+e.Width/2, e.Y+e.Height/2, math.Abs(e.Width/2), math.Abs(e.Height/2))
			case "diamond":
				dc.MoveTo(e.X+e.Width/2, e.Y)
				dc.LineTo(e.X+e.Width, e.Y+e.Height/2)
				dc.LineTo(e.X+e.Width/2, e.Y+e.Height)
				dc.LineTo(e.X, e.Y+e.Height/2)
				dc.ClosePath()
			default:
				r := 0.0
				if len(e.Roundness) > 0 && string(e.Roundness) != "null" && string(e.Roundness) != "false" {
					r = math.Min(math.Abs(e.Width), math.Abs(e.Height)) * .18
				}
				dc.DrawRoundedRectangle(e.X, e.Y, e.Width, e.Height, r)
			}
			if e.BgColor != "" && e.BgColor != "transparent" {
				dc.SetColor(previewColor(e.BgColor, opacity))
				dc.FillPreserve()
			}
			dc.SetColor(previewColor(stroke, opacity))
			dc.Stroke()
		case "arrow", "line", "freedraw":
			for i, p := range e.Points {
				if i == 0 {
					dc.MoveTo(e.X+p[0], e.Y+p[1])
				} else {
					dc.LineTo(e.X+p[0], e.Y+p[1])
				}
			}
			if len(e.Points) == 1 {
				dc.LineTo(e.X+e.Points[0][0]+.5, e.Y+e.Points[0][1]+.5)
			}
			dc.Stroke()
			if e.Type == "arrow" && len(e.Points) > 1 {
				n := len(e.Points)
				if e.EndArrow != nil {
					previewArrow(dc, e, e.Points[n-1], e.Points[n-2])
				}
				if e.StartArrow != nil {
					previewArrow(dc, e, e.Points[0], e.Points[1])
				}
			}
		case "text":
			size := e.FontSize
			if size <= 0 {
				size = 20
			}
			face := truetype.NewFace(previewFont, &truetype.Options{Size: math.Min(512, size), DPI: 72})
			dc.SetFontFace(face)
			lh := e.LineHeight
			if lh <= 0 || lh > 10 {
				lh = 1.25
			}
			for i, line := range strings.Split(e.Text, "\n") {
				x := e.X
				lw, _ := dc.MeasureString(line)
				if e.TextAlign == "center" {
					x += (e.Width - lw) / 2
				} else if e.TextAlign == "right" {
					x += e.Width - lw
				}
				dc.DrawString(line, x, e.Y+float64(face.Metrics().Ascent)/64+float64(i)*size*lh)
			}
			face.Close()
		case "image":
			var data string
			if e.FileID != nil {
				data = scene.Files[*e.FileID].DataURL
			}
			img := previewImage(data)
			if img != nil {
				dc.Push()
				dc.Translate(e.X, e.Y)
				dc.Scale(e.Width/float64(img.Bounds().Dx()), e.Height/float64(img.Bounds().Dy()))
				dc.DrawImage(img, 0, 0)
				dc.Pop()
			} else {
				// No network or file access when a scene references an unavailable image.
				dc.SetColor(previewColor("#868e96", opacity))
				dc.DrawRectangle(e.X, e.Y, e.Width, e.Height)
				dc.Stroke()
				dc.DrawLine(e.X, e.Y, e.X+e.Width, e.Y+e.Height)
				dc.Stroke()
			}
		}
		dc.Pop()
	}
	var out bytes.Buffer
	err := png.Encode(&out, dc.Image())
	return out.Bytes(), err
}

func previewColor(s string, opacity float64) color.NRGBA {
	if s == "transparent" {
		return color.NRGBA{}
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		n = 0x1e1e1e
	}
	return color.NRGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: uint8(math.Round(opacity * 2.55))}
}

func previewArrow(dc *gg.Context, e previewElement, tip, from []float64) {
	a := math.Atan2(tip[1]-from[1], tip[0]-from[0])
	size := 14.0
	x, y := e.X+tip[0], e.Y+tip[1]
	dc.SetDash()
	dc.MoveTo(x-size*math.Cos(a+.45), y-size*math.Sin(a+.45))
	dc.LineTo(x, y)
	dc.LineTo(x-size*math.Cos(a-.45), y-size*math.Sin(a-.45))
	dc.Stroke()
}

func previewImage(data string) image.Image {
	if !strings.HasPrefix(data, "data:image/") || len(data) > MaxSceneBytes {
		return nil
	}
	_, encoded, ok := strings.Cut(data, ";base64,")
	if !ok {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return nil
	}
	img, _, _ := image.Decode(bytes.NewReader(b))
	return img
}
