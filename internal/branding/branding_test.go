package branding

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"hawkeye/internal/validation"
)

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solid(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 30, 90, 160, 255
	}
	return img
}

func TestAPNGOrJPEGLogoIsAcceptedAndCleaned(t *testing.T) {
	l, err := NormalizeLogo(pngBytes(t, solid(300, 90)))
	if err != nil || l.Type != "png" || l.Width != 300 || l.Height != 90 {
		t.Fatalf("png: %v %+v", err, l)
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, solid(120, 120), nil); err != nil {
		t.Fatal(err)
	}
	l, err = NormalizeLogo(jb.Bytes())
	if err != nil || l.Type != "jpeg" || l.Width != 120 {
		t.Fatalf("jpeg: %v %+v", err, l)
	}
	if _, err := jpeg.Decode(bytes.NewReader(l.Data)); err != nil {
		t.Fatalf("the stored JPEG must decode: %v", err)
	}
}

func TestA16BitPNGWithTransparencyIsStoredAs8BitSoThePDFLibraryCanUseIt(t *testing.T) {
	img := image.NewNRGBA64(image.Rect(0, 0, 50, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 50; x++ {
			img.SetNRGBA64(x, y, color.NRGBA64{R: 40000, G: 100, B: 65000, A: uint16(x * 1300)})
		}
	}
	l, err := NormalizeLogo(pngBytes(t, img))
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(l.Data))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(*image.NRGBA); !ok {
		t.Fatalf("stored as %T, want 8-bit NRGBA", out)
	}
}

func TestExtraBytesHiddenAfterTheImageAreDropped(t *testing.T) {
	raw := append(pngBytes(t, solid(40, 40)), []byte("<script>alert(1)</script> SECRET-MARKER")...)
	l, err := NormalizeLogo(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(l.Data, []byte("SECRET-MARKER")) || bytes.Contains(l.Data, []byte("<script")) {
		t.Fatal("the stored logo still carries the extra bytes: it must be re-encoded, not stored as uploaded")
	}
}

func TestOtherFormatsAndHostileFilesAreRefused(t *testing.T) {
	var g bytes.Buffer
	gif.Encode(&g, solid(10, 10), nil)
	big := solid(2001, 10)
	area := solid(1999, 1999) // 4 million pixels in a few kilobytes of PNG

	cases := map[string][]byte{
		"empty":         nil,
		"svg":           []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":          []byte("<html><body>hi</body></html>"),
		"gif":           g.Bytes(),
		"text":          []byte("not an image"),
		"truncated png": pngBytes(t, solid(40, 40))[:60],
		"too wide":      pngBytes(t, big),
		"too many pixels (a small file that would expand to a huge image)": pngBytes(t, area),
		"over the size limit": append(pngBytes(t, solid(10, 10)), make([]byte, MaxLogoBytes)...),
	}
	for name, raw := range cases {
		_, err := NormalizeLogo(raw)
		var ve *validation.Error
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
}

func TestDisplayNames(t *testing.T) {
	if got, err := CleanName("  Northwind Digital  "); err != nil || got != "Northwind Digital" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := CleanName("Müller & Söhne — Web"); err != nil || got != "Müller & Söhne — Web" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{strings.Repeat("a", 101), "two\nlines", "tab\there", "bell\a", "nul\x00"} {
		if _, err := CleanName(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, err := CleanName("   "); err != nil || got != "" {
		t.Fatalf("blank clears the name: %q %v", got, err)
	}
}
