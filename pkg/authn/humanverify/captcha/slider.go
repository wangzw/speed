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

const (
	sliderWidth     = 320
	sliderHeight    = 160
	pieceSizeMin    = 40
	pieceSizeMax    = 56
	sliderTolerance = 2
)

type sliderImages struct {
	background []byte
	piece      []byte
	digests    []string
}

type intDrawer func(min, max int) (int, error)

func newSlider(salt string) (sliderImages, error) {
	return newSliderWithDrawer(salt, randomInt)
}

func newSliderWithDrawer(salt string, draw intDrawer) (sliderImages, error) {
	pieceSize, err := randomInt(pieceSizeMin, pieceSizeMax)
	if err != nil {
		return sliderImages{}, err
	}
	background, err := drawBackground()
	if err != nil {
		return sliderImages{}, err
	}
	pieceTop := (sliderHeight - pieceSize) / 2
	// The piece's left edge stays clear of the background's edges, and the cut leaves room for the whole tolerance window on the upper side, so the accepted offsets are all positions the piece can occupy. The lower side needs no such room because pieceSize is well above sliderTolerance.
	cut, err := draw(pieceSize, sliderWidth-pieceSize-sliderTolerance)
	if err != nil {
		return sliderImages{}, err
	}

	piece := cutPiece(background, cut, pieceTop, pieceSize)
	// The piece and the background are each noised after the cut, so the piece does not match the hole pixel for pixel even at the right offset.
	if noiseErr := addImageNoise(piece); noiseErr != nil {
		return sliderImages{}, noiseErr
	}
	drawHole(background, cut+pieceSize/2, pieceTop+pieceSize/2, pieceSize)
	if noiseErr := addImageNoise(background); noiseErr != nil {
		return sliderImages{}, noiseErr
	}

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
		digests = append(digests, digest(salt, strconv.Itoa(offset)))
	}
	return sliderImages{background: backgroundPNG, piece: piecePNG, digests: digests}, nil
}

func drawBackground() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, sliderWidth, sliderHeight))
	r0, err := randomInt(10, 70)
	if err != nil {
		return nil, err
	}
	g0, err := randomInt(20, 80)
	if err != nil {
		return nil, err
	}
	b0, err := randomInt(30, 90)
	if err != nil {
		return nil, err
	}
	dr, err := randomInt(70, 150)
	if err != nil {
		return nil, err
	}
	dg, err := randomInt(70, 150)
	if err != nil {
		return nil, err
	}
	db, err := randomInt(70, 150)
	if err != nil {
		return nil, err
	}
	for y := 0; y < sliderHeight; y++ {
		t := float64(y) / float64(sliderHeight-1)
		line := color.RGBA{
			R: clampByte(r0 + int(float64(dr)*t)),
			G: clampByte(g0 + int(float64(dg)*t)),
			B: clampByte(b0 + int(float64(db)*t)),
			A: 255,
		}
		for x := 0; x < sliderWidth; x++ {
			img.SetRGBA(x, y, line)
		}
	}
	circles, err := randomInt(14, 24)
	if err != nil {
		return nil, err
	}
	for i := 0; i < circles; i++ {
		var cx, cy, radius int
		cx, err = randomInt(-pieceSizeMax, sliderWidth+pieceSizeMax)
		if err != nil {
			return nil, err
		}
		cy, err = randomInt(-pieceSizeMax, sliderHeight+pieceSizeMax)
		if err != nil {
			return nil, err
		}
		radius, err = randomInt(8, 46)
		if err != nil {
			return nil, err
		}
		var shade uint8
		shade, err = randomByte()
		if err != nil {
			return nil, err
		}
		// The bytes are shifted into the upper half of the range so the circles read as light over the dark gradient; byte arithmetic wraps, which makes the channels differ.
		shade = 128 + shade%96
		fillCircle(img, cx, cy, radius, color.RGBA{
			R: shade,
			G: shade * 3,
			B: shade * 5,
			A: 96,
		})
	}
	lines, err := randomInt(4, 9)
	if err != nil {
		return nil, err
	}
	for i := 0; i < lines; i++ {
		var x0, x1, y0, y1 int
		x0, err = randomInt(0, sliderWidth)
		if err != nil {
			return nil, err
		}
		x1, err = randomInt(0, sliderWidth)
		if err != nil {
			return nil, err
		}
		y0, err = randomInt(0, sliderHeight)
		if err != nil {
			return nil, err
		}
		y1, err = randomInt(0, sliderHeight)
		if err != nil {
			return nil, err
		}
		drawLine(img, x0, y0, x1, y1, color.RGBA{R: 220, G: 232, B: 245, A: 56})
	}
	return img, nil
}

func cutPiece(background *image.RGBA, left, top, size int) *image.RGBA {
	piece := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(piece, piece.Bounds(), background, image.Pt(left, top), draw.Src)

	centre := size / 2
	radius := centre - 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
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

func drawHole(background *image.RGBA, cx, cy, size int) {
	fillCircle(background, cx, cy, size/2-2, color.RGBA{R: 18, G: 24, B: 38, A: 208})
	fillCircle(background, cx, cy, size/2-2, color.RGBA{R: 120, G: 142, B: 170, A: 34})
}

func addImageNoise(img *image.RGBA) error {
	amplitude, err := randomInt(6, 14)
	if err != nil {
		return err
	}
	bounds := img.Bounds()
	noise := make([]byte, bounds.Dx()*bounds.Dy())
	if _, err := rand.Read(noise); err != nil {
		return err
	}
	span := 2*amplitude + 1
	i := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			delta := int(noise[i])%span - amplitude
			i++
			c := img.RGBAAt(x, y)
			c.R = clampByte(int(c.R) + delta)
			c.G = clampByte(int(c.G) + delta)
			c.B = clampByte(int(c.B) + delta)
			img.SetRGBA(x, y, c)
		}
	}
	return nil
}

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

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

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

func randomFloat(min, max float64) (float64, error) {
	var raw [2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	unit := float64(binary.BigEndian.Uint16(raw[:])) / (1 << 16)
	return min + unit*(max-min), nil
}

func clampByte(value int) uint8 {
	switch {
	case value < 0:
		return 0
	case value > 255:
		return 255
	default:
		return uint8(value)
	}
}

func randomByte() (uint8, error) {
	var raw [1]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	return raw[0], nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
