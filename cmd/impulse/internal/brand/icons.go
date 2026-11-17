package brand

import (
	"bytes"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/go-playground/errors/v5"
)

// The sizes the logo is held to and the icons cut from it, in pixels a side.
const (
	// MinLogoSize is the smallest logo the command takes: the largest icon is cut from
	// the logo at this size, and an icon is only ever scaled down, never up.
	MinLogoSize = 512
	// maxLogoSize bounds the logo so that reading one never takes more memory than a
	// logo could need.
	maxLogoSize = 4096
	// faviconSize is the PNG favicon, three times the sixteen pixels a tab draws it in
	// on a dense screen, doubled.
	faviconSize = 96
	// The app icons the manifest names: the small one, the large one, and the large
	// one a device cuts to its own shape.
	smallIconSize = 192
	largeIconSize = 512
	// serverLogoSize is the logo the maintenance page shows, drawn at half that.
	serverLogoSize = 192
)

// safeZone is the part of a maskable icon a device never cuts away, as a share of its
// side: the logo is scaled into it, centered, on the background color.
const safeZone = 0.8

// logo is a brand's logo, read: its pixels as alpha-premultiplied 16-bit channels, row by
// row, which is the form an average of pixels is taken in.
type logo struct {
	size int
	// pix holds red, green, blue and alpha for each pixel.
	pix []float64
}

// readLogo reads the logo and holds it to its shape: a PNG, square, between MinLogoSize
// and maxLogoSize pixels a side. file is the logo's path as a refusal names it.
func readLogo(file string, data []byte) (*logo, error) {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.Newf("%s does not read as a PNG (%v): the logo is a square PNG at least %d pixels a side", file, err, MinLogoSize)
	}
	switch {
	case config.Width != config.Height:
		return nil, errors.Newf("%s is %d by %d pixels, which is not square: the logo is a square PNG at least %d by %d pixels, since every icon is square and is cut from it", file, config.Width, config.Height, MinLogoSize, MinLogoSize)
	case config.Width < MinLogoSize:
		return nil, errors.Newf("%s is %d by %d pixels, which is too small: the logo is a square PNG at least %d by %d pixels, since the largest icon is cut from it at that size and an icon is never scaled up", file, config.Width, config.Height, MinLogoSize, MinLogoSize)
	case config.Width > maxLogoSize:
		return nil, errors.Newf("%s is %d by %d pixels, which is more than any icon needs: the logo is a square PNG between %d and %d pixels a side", file, config.Width, config.Height, MinLogoSize, maxLogoSize)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.Newf("%s does not read as a PNG (%v): the logo is a square PNG at least %d pixels a side", file, err, MinLogoSize)
	}

	size := config.Width
	bounds := img.Bounds()
	l := &logo{size: size, pix: make([]float64, 0, size*size*channels)}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			l.pix = append(l.pix, float64(r), float64(g), float64(b), float64(a))
		}
	}

	return l, nil
}

// channels is the number of channels a pixel holds: red, green, blue and alpha.
const channels = 4

// span is one source pixel a target pixel covers along an axis, and the share of the
// source pixel it covers.
type span struct {
	index  int
	weight float64
}

// spans lists, for each of the to target pixels along an axis of from source pixels, the
// source pixels it covers and how much of each: a target pixel covers from/to source
// pixels, whole ones in the middle and a part of the one at either end.
func spans(from, to int) [][]span {
	step := float64(from) / float64(to)
	all := make([][]span, to)
	for t := range to {
		start, end := float64(t)*step, float64(t+1)*step
		first, last := int(math.Floor(start)), int(math.Ceil(end))
		for s := first; s < last && s < from; s++ {
			weight := math.Min(end, float64(s+1)) - math.Max(start, float64(s))
			if weight > 0 {
				all[t] = append(all[t], span{index: s, weight: weight})
			}
		}
	}

	return all
}

// scaled is the logo scaled down to size pixels a side: every target pixel is the average
// of the source pixels it covers, each weighted by the part of it covered, taken over
// premultiplied channels so a transparent pixel adds no color. The result holds the same
// premultiplied 16-bit channels.
func (l *logo) scaled(size int) *logo {
	if size == l.size {
		return l
	}
	across := spans(l.size, size)
	out := &logo{size: size, pix: make([]float64, size*size*channels)}
	area := (float64(l.size) / float64(size)) * (float64(l.size) / float64(size))
	for ty, rows := range across {
		for tx, columns := range across {
			var sum [channels]float64
			for _, row := range rows {
				for _, column := range columns {
					at := (row.index*l.size + column.index) * channels
					weight := row.weight * column.weight
					for c := range channels {
						sum[c] += l.pix[at+c] * weight
					}
				}
			}
			at := (ty*size + tx) * channels
			for c := range channels {
				out.pix[at+c] = sum[c] / area
			}
		}
	}

	return out
}

// The range of a 16-bit channel and of the 8-bit channel a PNG is written in.
const (
	full16 = 0xffff
	full8  = 0xff
)

// image is the logo as an image a PNG is written from: 8-bit channels, not premultiplied.
func (l *logo) image() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, l.size, l.size))
	for i := range l.size * l.size {
		at := i * channels
		alpha := l.pix[at+3]
		if alpha <= 0 {
			continue
		}
		for c := range 3 {
			img.Pix[at+c] = eightBit(l.pix[at+c] / alpha)
		}
		img.Pix[at+3] = eightBit(alpha / full16)
	}

	return img
}

// eightBit is a channel's share of its range as an 8-bit value, rounded.
func eightBit(share float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(1, share)) * full8))
}

// icon is the logo as a PNG icon of size pixels a side.
func (l *logo) icon(size int) ([]byte, error) {
	return encode(l.scaled(size).image())
}

// maskable is the icon a device cuts to its own shape, size pixels a side: the logo scaled
// into the central safe zone on the background color, which fills the rest, so nothing
// of the logo is cut away whatever the shape.
func (l *logo) maskable(size int, background string) ([]byte, error) {
	fill, err := parseColor(background)
	if err != nil {
		return nil, err
	}
	// The inner side keeps the size's parity, so the margin is the same on every side.
	inner := int(math.Round(float64(size) * safeZone))
	if (size-inner)%2 != 0 {
		inner--
	}
	margin := (size - inner) / 2
	mark := l.scaled(inner)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			at := (y*size + x) * channels
			img.Pix[at], img.Pix[at+1], img.Pix[at+2], img.Pix[at+3] = fill.R, fill.G, fill.B, full8
			if x < margin || x >= margin+inner || y < margin || y >= margin+inner {
				continue
			}
			from := ((y-margin)*inner + (x - margin)) * channels
			// The logo over the background: its premultiplied color, plus the
			// background where the logo is transparent.
			through := 1 - mark.pix[from+3]/full16
			for c, under := range [3]uint8{fill.R, fill.G, fill.B} {
				img.Pix[at+c] = eightBit(mark.pix[from+c]/full16 + float64(under)/full8*through)
			}
		}
	}

	return encode(img)
}

// encode writes the image as a PNG, compressed as far as the format goes: an icon is
// written once and fetched by every device.
func encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&b, img); err != nil {
		return nil, errors.Wrap(err, "png.Encoder.Encode()")
	}

	return b.Bytes(), nil
}

// shows reports whether the PNG shows the image: the same size and the same pixels. The
// comparison is of what the file shows and not of its bytes, which change with the
// encoder and not with the picture.
func shows(file []byte, img *image.NRGBA) bool {
	kept, err := png.Decode(bytes.NewReader(file))
	if err != nil || kept.Bounds() != img.Bounds() {
		return false
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if color.NRGBAModel.Convert(kept.At(x, y)) != img.NRGBAAt(x, y) {
				return false
			}
		}
	}

	return true
}

// parseColor reads a color written as a number sign and six hex digits.
func parseColor(written string) (color.NRGBA, error) {
	if !hexColor.MatchString(written) {
		return color.NRGBA{}, errors.Newf("%q is not a color written as a number sign and six hex digits", written)
	}
	rgb, err := hex.DecodeString(written[1:])
	if err != nil {
		return color.NRGBA{}, errors.Wrap(err, "hex.DecodeString()")
	}

	return color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: full8}, nil
}
