package branding

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	"hawkeye/internal/validation"
)

// Limits on an uploaded logo. They keep a stored logo small enough to attach to many PDFs, and keep decoding cheap: the pixel check runs on the
// header, before any pixel data is read, so a tiny file that expands to an enormous image is refused without being decoded.
const (
	MaxLogoBytes  = 512 << 10
	MaxLogoSide   = 2000
	MaxLogoPixels = 2_000_000
)

// Logo is a validated logo, ready to store and to draw.
type Logo struct {
	Data          []byte
	Type          string // "png" or "jpeg"
	Width, Height int
}

// NormalizeLogo checks an uploaded image and returns a clean copy. Only PNG and JPEG are accepted (never SVG, which can carry scripts). The image is
// decoded and written out again, which drops metadata and anything hidden in the file, and makes the encoding one the PDF library can embed
// (8-bit, not interlaced). The error is a validation error whose text can be shown to the person.
func NormalizeLogo(raw []byte) (Logo, error) {
	bad := func(msg string) (Logo, error) { return Logo{}, validation.New("logo", msg) }
	if len(raw) == 0 {
		return bad("choose an image file")
	}
	if len(raw) > MaxLogoBytes {
		return bad("is too large: use an image under 512 KB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		return bad("must be a PNG or JPEG image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxLogoSide || cfg.Height > MaxLogoSide || cfg.Width*cfg.Height > MaxLogoPixels {
		return bad("is too large in pixels: use an image no bigger than 2000 x 2000")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return bad("could not be read: try saving it again as a PNG")
	}
	var out bytes.Buffer
	switch format {
	case "png":
		flat := image.NewNRGBA(img.Bounds())
		draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Src)
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, flat); err != nil {
			return bad("could not be processed: try saving it again as a PNG")
		}
	case "jpeg":
		flat := image.NewRGBA(img.Bounds())
		draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Src)
		if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: 90}); err != nil {
			return bad("could not be processed: try saving it again as a JPEG")
		}
	}
	if out.Len() > MaxLogoBytes {
		return bad("is too large: use an image under 512 KB")
	}
	return Logo{Data: out.Bytes(), Type: format, Width: cfg.Width, Height: cfg.Height}, nil
}
