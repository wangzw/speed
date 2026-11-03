package captcha

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
)

// The slider's geometry: a background of a fixed size, a piece cut from a fixed
// vertical position, and the tolerance by which a client's offset may miss the
// cut position. The horizontal cut position is drawn fresh for every challenge
// and is the answer.
const (
	sliderWidth     = 320
	sliderHeight    = 160
	pieceSize       = 48
	sliderTolerance = 4
)

// sliderImages is what one slider challenge is served with.
type sliderImages struct {
	background []byte
	piece      []byte
	// digests are the accepted offsets, one digest each, so the cut position
	// itself is not stored.
	digests []string
}

// newSlider draws a background, cuts a piece out of it and records the digests
// of the offsets that count as the right answer.
func newSlider() (sliderImages, error) {
	background, err := drawBackground()
	if err != nil {
		return sliderImages{}, err
	}
	pieceTop := (sliderHeight - pieceSize) / 2
	// The piece's left edge stays clear of the background's own edges, so the
	// accepted offsets are all positions the piece can actually occupy.
	cut, err := randomInt(pieceSize, sliderWidth-pieceSize)
	if err != nil {
		return sliderImages{}, err
	}

	piece := cutPiece(background, cut, pieceTop)
	drawHole(background, cut+pieceSize/2, pieceTop+pieceSize/2)

	backgroundPNG, err := encodePNG(background)
	if err != nil {
		return sliderImages{}, err
	}
	piecePNG, err := encodePNG(piece)
	if err != nil {
		return sliderImages{}, err
	}

	digests := make([]string, 0, 2*sliderTolerance+1)
	for offset := cut - sliderTolerance; offset <= cut+sliderTolerance; offset++ {
		if offset < 0 || offset > sliderWidth-pieceSize {
			continue
		}
		digests = append(digests, digest(strconv.Itoa(offset)))
	}
	return sliderImages{background: backgroundPNG, piece: piecePNG, digests: digests}, nil
}

// drawBackground paints a vertical gradient with translucent circles and
// diagonal lines over it, so the piece carries texture rather than a flat
// colour.
func drawBackground() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, sliderWidth, sliderHeight))
	for y := 0; y < sliderHeight; y++ {
		t := float64(y) / float64(sliderHeight-1)
		line := color.RGBA{
			R: uint8(24 + 92*t),
			G: uint8(42 + 118*t),
			B: uint8(66 + 138*t),
			A: 255,
		}
		for x := 0; x < sliderWidth; x++ {
			img.SetRGBA(x, y, line)
		}
	}
	for i := 0; i < 18; i++ {
		cx, err := randomInt(-pieceSize, sliderWidth+pieceSize)
		if err != nil {
			return nil, err
		}
		cy, err := randomInt(-pieceSize, sliderHeight+pieceSize)
		if err != nil {
			return nil, err
		}
		radius, err := randomInt(10, 46)
		if err != nil {
			return nil, err
		}
		shade, err := randomByte()
		if err != nil {
			return nil, err
		}
		// The bytes are shifted into the upper half of the range, so the
		// circles read as light over the dark gradient. Byte arithmetic wraps,
		// which is what makes the channels differ.
		shade = 128 + shade%96
		fillCircle(img, cx, cy, radius, color.RGBA{
			R: shade,
			G: shade * 3,
			B: shade * 5,
			A: 96,
		})
	}
	for i := 0; i < 6; i++ {
		x0, err := randomInt(0, sliderWidth)
		if err != nil {
			return nil, err
		}
		x1, err := randomInt(0, sliderWidth)
		if err != nil {
			return nil, err
		}
		y0, err := randomInt(0, sliderHeight)
		if err != nil {
			return nil, err
		}
		y1, err := randomInt(0, sliderHeight)
		if err != nil {
			return nil, err
		}
		drawLine(img, x0, y0, x1, y1, color.RGBA{R: 220, G: 232, B: 245, A: 56})
	}
	return img, nil
}

// cutPiece copies the square at (left, top) into a round piece with a light
// border. Everything outside the circle is transparent.
func cutPiece(background *image.RGBA, left, top int) *image.RGBA {
	piece := image.NewRGBA(image.Rect(0, 0, pieceSize, pieceSize))
	draw.Draw(piece, piece.Bounds(), background, image.Pt(left, top), draw.Src)

	centre := pieceSize / 2
	radius := centre - 2
	for y := 0; y < pieceSize; y++ {
		for x := 0; x < pieceSize; x++ {
			dx, dy := x-centre, y-centre
			distance := dx*dx + dy*dy
			switch {
			case distance > radius*radius:
				piece.SetRGBA(x, y, color.RGBA{})
			case distance > (radius-3)*(radius-3):
				piece.SetRGBA(x, y, color.RGBA{R: 246, G: 250, B: 255, A: 235})
			}
		}
	}
	return piece
}

// drawHole darkens the round area the piece was cut from, so the cut position is
// a visible gap rather than a texture the piece still matches exactly.
func drawHole(background *image.RGBA, cx, cy int) {
	fillCircle(background, cx, cy, pieceSize/2-2, color.RGBA{R: 18, G: 24, B: 38, A: 208})
	fillCircle(background, cx, cy, pieceSize/2-2, color.RGBA{R: 120, G: 142, B: 170, A: 34})
}

// fillCircle blends col over the points of dst within radius of (cx, cy).
func fillCircle(dst *image.RGBA, cx, cy, radius int, col color.RGBA) {
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			if !image.Pt(x, y).In(dst.Bounds()) {
				continue
			}
			dst.SetRGBA(x, y, blend(dst.RGBAAt(x, y), col))
		}
	}
}

// drawLine blends col along the line from (x0, y0) to (x1, y1).
func drawLine(dst *image.RGBA, x0, y0, x1, y1 int, col color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	stepX, stepY := 1, 1
	if x0 > x1 {
		stepX = -1
	}
	if y0 > y1 {
		stepY = -1
	}
	err := dx + dy
	for {
		if image.Pt(x0, y0).In(dst.Bounds()) {
			dst.SetRGBA(x0, y0, blend(dst.RGBAAt(x0, y0), col))
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		twice := 2 * err
		if twice >= dy {
			err += dy
			x0 += stepX
		}
		if twice <= dx {
			err += dx
			y0 += stepY
		}
	}
}

// blend composites src over dst with src's alpha.
func blend(dst, src color.RGBA) color.RGBA {
	sa := float64(src.A) / 255
	da := float64(dst.A) / 255
	out := sa + da*(1-sa)
	if out == 0 {
		return color.RGBA{}
	}
	return color.RGBA{
		R: uint8((float64(src.R)*sa + float64(dst.R)*da*(1-sa)) / out),
		G: uint8((float64(src.G)*sa + float64(dst.G)*da*(1-sa)) / out),
		B: uint8((float64(src.B)*sa + float64(dst.B)*da*(1-sa)) / out),
		A: uint8(out * 255),
	}
}

// encodePNG encodes an image.
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// randomInt draws a value in [min, max]. The modulo bias is immaterial for the
// ranges here.
func randomInt(min, max int) (int, error) {
	if max <= min {
		return min, nil
	}
	var raw [2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	value := int(binary.BigEndian.Uint16(raw[:]))
	return min + value%(max-min+1), nil
}

// randomByte draws one random byte.
func randomByte() (uint8, error) {
	var raw [1]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	return raw[0], nil
}

// abs is the absolute value of n.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
