package brand

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// gray is a logo for a test whose every pixel is opaque with the same value in each
// color channel: the values row by row, as shares of the channel's range.
func gray(size int, values ...float64) *logo {
	l := &logo{size: size, pix: make([]float64, 0, len(values)*channels)}
	for _, v := range values {
		l.pix = append(l.pix, v*full16, v*full16, v*full16, full16)
	}

	return l
}

// TestScaled holds the downscale to what it is: each target pixel is the average of the
// source pixels it covers, a pixel covered in part counted by the part covered.
func TestScaled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  *logo
		size int
		// want is the red channel of each target pixel, row by row, as a share of its range.
		want []float64
	}{
		{
			name: "four to two: each target pixel is the mean of its block of four",
			src: gray(4,
				0, 0.2, 0.4, 0.6,
				0.2, 0.2, 0.6, 0.6,
				1, 1, 0, 0,
				1, 1, 0, 0.4),
			size: 2,
			want: []float64{0.15, 0.55, 1, 0.1},
		},
		{
			name: "three to two: a target pixel covers one source pixel and half of the next",
			src: gray(3,
				0, 0.3, 0.6,
				0.3, 0.6, 0.9,
				0.6, 0.9, 0.3),
			size: 2,
			// The first target pixel covers source pixel (0,0) whole, half of (1,0) and
			// of (0,1), and a quarter of (1,1): (0 + 0.15 + 0.15 + 0.15) / 2.25.
			want: []float64{0.45 / 2.25, (0.6 + 0.15 + 0.45 + 0.15) / 2.25, (0.6 + 0.15 + 0.45 + 0.15) / 2.25, (0.3 + 0.45 + 0.45 + 0.15) / 2.25},
		},
		{
			name: "the same size is the logo as it is",
			src:  gray(2, 0.1, 0.2, 0.3, 0.4),
			size: 2,
			want: []float64{0.1, 0.2, 0.3, 0.4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.src.scaled(tt.size)
			if got.size != tt.size || len(got.pix) != tt.size*tt.size*channels {
				t.Fatalf("scaled(%d) is %d pixels a side holding %d values", tt.size, got.size, len(got.pix))
			}
			red := make([]float64, 0, len(tt.want))
			for i := range tt.size * tt.size {
				red = append(red, math.Round(got.pix[i*channels]/full16*1e6)/1e6)
				if alpha := got.pix[i*channels+3]; math.Abs(alpha-full16) > 1e-6 {
					t.Errorf("pixel %d: alpha = %v, want opaque", i, alpha)
				}
			}
			want := make([]float64, 0, len(tt.want))
			for _, w := range tt.want {
				want = append(want, math.Round(w*1e6)/1e6)
			}
			if diff := cmp.Diff(want, red); diff != "" {
				t.Errorf("scaled(%d) mismatch (-want +got):\n%s", tt.size, diff)
			}
		})
	}
}

// TestScaledKeepsColorUnderTransparency holds the average to premultiplied channels: a
// transparent pixel adds no color to the pixels beside it, whatever color it carries, so
// a logo's edge fades and never darkens.
func TestScaledKeepsColorUnderTransparency(t *testing.T) {
	t.Parallel()

	// Two opaque red pixels beside two fully transparent ones.
	src := &logo{size: 2, pix: []float64{
		full16, 0, 0, full16, 0, 0, 0, 0,
		full16, 0, 0, full16, 0, 0, 0, 0,
	}}
	got := src.scaled(1).image()
	if r, g, b, a := got.Pix[0], got.Pix[1], got.Pix[2], got.Pix[3]; r != full8 || g != 0 || b != 0 || a != 128 {
		t.Errorf("the average of red and transparent = (%d, %d, %d, %d), want full red at half alpha (255, 0, 0, 128)", r, g, b, a)
	}
}

// TestSpans holds the spans along an axis to covering every source pixel once in all.
func TestSpans(t *testing.T) {
	t.Parallel()

	for _, sizes := range [][2]int{{512, 192}, {640, 410}, {4096, 96}, {513, 512}, {512, 512}} {
		from, to := sizes[0], sizes[1]
		covered := make([]float64, from)
		for _, target := range spans(from, to) {
			total := 0.0
			for _, s := range target {
				covered[s.index] += s.weight
				total += s.weight
			}
			if want := float64(from) / float64(to); math.Abs(total-want) > 1e-9 {
				t.Fatalf("%d to %d: a target pixel covers %v source pixels, want %v", from, to, total, want)
			}
		}
		for i, c := range covered {
			if math.Abs(c-1) > 1e-9 {
				t.Fatalf("%d to %d: source pixel %d is covered %v times, want once", from, to, i, c)
			}
		}
	}
}
