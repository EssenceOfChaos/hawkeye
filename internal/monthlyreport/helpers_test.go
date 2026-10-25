package monthlyreport_test

import (
	"image"
	"image/png"
	"io"
)

func imageOf(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 30, 90, 160, 255
	}
	return img
}

func encodePNG(w io.Writer, img image.Image) error { return png.Encode(w, img) }
