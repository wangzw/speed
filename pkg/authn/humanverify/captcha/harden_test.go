package captcha

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	libcaptcha "github.com/mojocn/base64Captcha"
)

// pngSize reads the pixel dimensions of a PNG body.
func pngSize(t *testing.T, body []byte) (width, height int) {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decoding the PNG geometry: %v", err)
	}
	return cfg.Width, cfg.Height
}

// TestTheImageFormVariesItsGeometry holds the image hardening: the height and
// width are drawn per challenge from the declared ranges, so successive
// challenges do not share one geometry a template matcher could key on.
func TestTheImageFormVariesItsGeometry(t *testing.T) {
	p, _ := newProvider(t)
	sizes := make(map[string]bool)
	for i := 0; i < 5; i++ {
		issued := issue(t, p, FormImage)
		media, err := p.Media(t.Context(), issued.ID, MediaImage)
		if err != nil {
			t.Fatalf("Media: %v", err)
		}
		width, height := pngSize(t, media.Body)
		if width < digitWidthMin || width > digitWidthMax {
			t.Errorf("the image width %d is outside [%d, %d]", width, digitWidthMin, digitWidthMax)
		}
		if height < digitHeightMin || height > digitHeightMax {
			t.Errorf("the image height %d is outside [%d, %d]", height, digitHeightMin, digitHeightMax)
		}
		sizes[fmt.Sprintf("%dx%d", width, height)] = true
	}
	if len(sizes) < 2 {
		t.Fatalf("five image challenges share one geometry (%v); the size is not drawn per challenge", sizes)
	}
}

// TestTheSliderPieceSizeVaries holds the slider's geometry hardening: the piece
// keeps no constant silhouette across challenges, so the size is drawn per
// challenge from the declared range.
func TestTheSliderPieceSizeVaries(t *testing.T) {
	p, _ := newProvider(t)
	sizes := make(map[int]bool)
	for i := 0; i < 5; i++ {
		issued := issue(t, p, FormSlider)
		media, err := p.Media(t.Context(), issued.ID, MediaPiece)
		if err != nil {
			t.Fatalf("Media: %v", err)
		}
		width, height := pngSize(t, media.Body)
		if width != height {
			t.Errorf("the piece is %dx%d, not a square", width, height)
		}
		if width < pieceSizeMin || width > pieceSizeMax {
			t.Errorf("the piece size %d is outside [%d, %d]", width, pieceSizeMin, pieceSizeMax)
		}
		sizes[width] = true
	}
	if len(sizes) < 2 {
		t.Fatalf("five slider challenges share one piece size (%v); the size is not drawn per challenge", sizes)
	}
}

// TestTheSliderNoisesItsImages holds the slider's noise hardening: a uniform
// image becomes per-pixel non-uniform, the amplitude stays inside the declared
// range, the same delta reaches all three colour channels and alpha is left
// alone, and two draws do not share one noise field.
func TestTheSliderNoisesItsImages(t *testing.T) {
	const base = 128
	uniform := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.SetRGBA(x, y, color.RGBA{R: base, G: base, B: base, A: 255})
			}
		}
		return img
	}

	first := uniform()
	if err := addImageNoise(first); err != nil {
		t.Fatalf("addImageNoise: %v", err)
	}
	second := uniform()
	if err := addImageNoise(second); err != nil {
		t.Fatalf("addImageNoise: %v", err)
	}

	maxDelta := 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := first.RGBAAt(x, y)
			if c.A != 255 {
				t.Fatalf("the noise changed the alpha at (%d, %d) to %d", x, y, c.A)
			}
			dr, dg, db := int(c.R)-base, int(c.G)-base, int(c.B)-base
			if dr != dg || dg != db {
				t.Fatalf("the noise reached the channels differently at (%d, %d): %d, %d, %d", x, y, dr, dg, db)
			}
			if abs(dr) > maxDelta {
				maxDelta = abs(dr)
			}
		}
	}
	if maxDelta < 6 {
		t.Errorf("the strongest noise deviation is %d, want at least 6; the image was not noised", maxDelta)
	}
	if maxDelta > 14 {
		t.Errorf("the strongest noise deviation is %d, want at most 14", maxDelta)
	}
	if bytes.Equal(pngOf(t, first), pngOf(t, second)) {
		t.Error("two noise draws produced the same image; the noise field is not drawn per image")
	}
}

// pngOf encodes an image, so two draws can be compared as bytes.
func pngOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	encoded, err := encodePNG(img)
	if err != nil {
		t.Fatalf("encodePNG: %v", err)
	}
	return encoded
}

// TestTheAudioIsPostProcessed holds the audio hardening: the body the route
// serves is not what the library drew, the post-processing is not one fixed
// transform, and the result is still the wave format the route states.
func TestTheAudioIsPostProcessed(t *testing.T) {
	driver := libcaptcha.NewDriverAudio(audioLength, DefaultAudioLanguage)
	_, question, _ := driver.GenerateIdQuestionAnswer()
	item, err := driver.DrawCaptcha(question)
	if err != nil {
		t.Fatalf("drawing the library audio: %v", err)
	}
	drawn, err := itemBytes(item)
	if err != nil {
		t.Fatalf("reading the library audio: %v", err)
	}

	first, err := hardenAudio(drawn, DefaultAudioLanguage)
	if err != nil {
		t.Fatalf("hardenAudio: %v", err)
	}
	if bytes.Equal(first, drawn) {
		t.Fatal("the served audio is the library's bytes; the audio was not post-processed")
	}
	if !bytes.HasPrefix(first, []byte("RIFF")) || !bytes.HasPrefix(first[8:], []byte("WAVE")) {
		t.Error("the hardened audio is not a RIFF/WAVE file")
	}

	second, err := hardenAudio(drawn, DefaultAudioLanguage)
	if err != nil {
		t.Fatalf("hardenAudio: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("two hardenings of one library audio are identical; the post-processing is a fixed transform")
	}
}
