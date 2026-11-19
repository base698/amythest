package messages

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Demo pictures are drawn here rather than shipped, so the seed carries no
// real photos: an oscilloscope trace and a small status dashboard.

func scopeTrace() []byte {
	const w, h = 480, 270
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{8, 18, 14, 255}
			if x%48 == 0 || y%45 == 0 {
				c = color.RGBA{40, 70, 60, 255}
			}
			wave := float64(h)/2 - 80*math.Sin(float64(x)/28)*math.Exp(-float64(x)/600)
			step := float64(h)/2 + 50
			if (x/40)%2 == 0 {
				step = float64(h)/2 - 50
			}
			if math.Abs(float64(y)-step) < 1.6 && x%3 != 0 {
				c = color.RGBA{255, 200, 70, 255}
			}
			if math.Abs(float64(y)-wave) < 2.2 {
				c = color.RGBA{90, 255, 140, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return encodeSeedPNG(img)
}

func dashboard() []byte {
	const w, h = 480, 300
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bars := []int{120, 180, 90, 210, 160, 60}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{246, 247, 249, 255}
			switch {
			case y < 36:
				c = color.RGBA{43, 52, 80, 255}
			case y > 60 && y < 280 && x > 30 && x < 450:
				c = color.RGBA{255, 255, 255, 255}
				i := (x - 50) / 66
				if i >= 0 && i < len(bars) && (x-50)%66 < 40 && y > 260-bars[i] && y < 260 {
					c = color.RGBA{70, 130, 220, 255}
					if i == 3 {
						c = color.RGBA{220, 90, 80, 255}
					}
				}
				if y == 260 {
					c = color.RGBA{180, 185, 195, 255}
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return encodeSeedPNG(img)
}

func encodeSeedPNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}
