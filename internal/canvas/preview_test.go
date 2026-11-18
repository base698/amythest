package canvas

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func previewPixels(t *testing.T, data []byte) (image.Image, int, int, int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	dark, red, blue := 0, 0, 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.R < 90 && c.G < 90 && c.B < 90 {
				dark++
			}
			if c.R > 180 && c.G < 90 && c.B < 90 {
				red++
			}
			if c.B > 180 && c.R < 90 && c.G < 90 {
				blue++
			}
		}
	}
	return img, dark, red, blue
}

func TestNativeCanvasPreviewExistsBeforeBrowserAndFollowsEdits(t *testing.T) {
	s, _ := New(t.TempDir())
	m, _ := s.Create("Native drawing", Placement{})
	m, err := s.ApplyOps(m.ID, Ops{Upsert: []map[string]any{
		{"id": "shape", "type": "rectangle", "x": -150, "y": -100, "width": 100, "height": 80, "backgroundColor": "#ff0000"},
		{"id": "label", "type": "text", "x": -140, "y": 0, "width": 160, "height": 30, "text": "Saved natively", "fontSize": 20},
	}})
	if err != nil {
		t.Fatal(err)
	}
	before, scene, _ := s.Scene(m.ID)
	if m.Drawing || !m.Stale {
		t.Fatal("fixture must have no browser rendering")
	}
	first, meta, err := s.Drawing(m.ID, "png")
	if err != nil {
		t.Fatal(err)
	}
	_, dark, red, _ := previewPixels(t, first)
	if dark < 100 || red < 1000 {
		t.Fatalf("missing shape/text: dark %d, red %d", dark, red)
	}
	second, _, err := s.Drawing(m.ID, "png")
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("cached preview changed")
	}
	after, unchanged, _ := s.Scene(m.ID)
	if meta.Rev != before.Rev || after != before || !bytes.Equal(scene, unchanged) {
		t.Fatal("preview mutated the drawing")
	}
	_, err = s.ApplyOps(m.ID, Ops{Upsert: []map[string]any{{"id": "shape", "backgroundColor": "#0000ff"}}})
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := s.Drawing(m.ID, "png")
	if err != nil {
		t.Fatal(err)
	}
	_, _, red, blue := previewPixels(t, next)
	if bytes.Equal(first, next) || blue < 1000 || red > 0 {
		t.Fatal("preview did not refresh after an edit")
	}
	_, err = s.ApplyOps(m.ID, Ops{Delete: []string{"shape", "label"}})
	if err != nil {
		t.Fatal(err)
	}
	empty, _, err := s.Drawing(m.ID, "png")
	if err != nil {
		t.Fatal(err)
	}
	_, dark, red, blue = previewPixels(t, empty)
	if dark+red+blue != 0 {
		t.Fatal("deleted shapes still appear")
	}
	// Browser exports retain their exact Excalidraw styling when current.
	latest, raw, _ := s.Scene(m.ID)
	_, err = s.Save(m.ID, SaveInput{BaseRev: latest.Rev, Scene: raw, PNG: first})
	if err != nil {
		t.Fatal(err)
	}
	browser, _, err := s.Drawing(m.ID, "png")
	if err != nil || !bytes.Equal(browser, first) {
		t.Fatal("browser rendering was replaced")
	}
}

func TestPreviewContainsShapesArrowInkTextAndEmbeddedImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var embedded bytes.Buffer
	png.Encode(&embedded, img)
	scene := map[string]any{
		"elements": []map[string]any{
			{"type": "ellipse", "x": -180, "y": -150, "width": 80, "height": 80, "backgroundColor": "#0000ff"},
			{"type": "rectangle", "x": 0, "y": -40, "width": 120, "height": 100, "angle": .3},
			{"type": "arrow", "x": -100, "y": -100, "points": [][]float64{{0, 0}, {110, 90}}, "endArrowhead": "arrow"},
			{"type": "freedraw", "x": -180, "y": 20, "points": [][]float64{{0, 0}, {20, 20}, {40, 0}}},
			{"type": "text", "x": -180, "y": 80, "width": 250, "height": 30, "fontSize": 22, "text": "Canvas preview"},
			{"type": "image", "x": 150, "y": 0, "width": 50, "height": 50, "fileId": "red"},
			{"type": "rectangle", "x": 100000, "y": 100000, "width": 200, "height": 100, "isDeleted": true},
		},
		"files": map[string]any{"red": map[string]string{"dataURL": "data:image/png;base64," + base64.StdEncoding.EncodeToString(embedded.Bytes())}},
	}
	raw, _ := json.Marshal(scene)
	b, err := previewPNG(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, dark, red, blue := previewPixels(t, b)
	if out.Bounds().Dx() > 900 || out.Bounds().Dy() > 650 || dark < 1000 || red < 1000 || blue < 1000 {
		t.Fatalf("incomplete preview: %v dark %d red %d blue %d", out.Bounds(), dark, red, blue)
	}
	huge, err := previewPNG([]byte(`{"elements":[{"type":"rectangle","x":-100000,"y":-100000,"width":200000,"height":100000}]}`))
	if err != nil {
		t.Fatal(err)
	}
	bounded, _, _, _ := previewPixels(t, huge)
	if bounded.Bounds().Dx() > 1600 || bounded.Bounds().Dy() > 1600 {
		t.Fatal("unbounded preview allocation")
	}
	for _, bad := range []string{`{"elements":[{"type":"rectangle","x":1e200}]}`, `{"elements":[{"type":"arrow","points":[[1]]}]}`} {
		if _, err := previewPNG([]byte(bad)); err == nil {
			t.Fatal("invalid geometry accepted")
		}
	}
}
