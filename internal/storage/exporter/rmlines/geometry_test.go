package rmlines

import (
	"bytes"
	"image"
	"math"
	"testing"
)

func TestComputeExtent(t *testing.T) {
	page := tracker{L: 0, T: 0, R: 1404, B: 1872, FW: 1404, FH: 1872}
	tests := []struct {
		name string
		ts   []tracker
		mul  float64
		pad  float64
		want Extent
	}{
		{"page only", []tracker{page}, 4, 30, Extent{0, 0, 1404, 1872}},
		{"grows down", []tracker{page, {L: 0, T: 0, R: 1404, B: 4112}}, 4, 30, Extent{0, 0, 1404, 4142}},
		{"grows left and right", []tracker{page, {L: -79, T: 0, R: 1476, B: 3196}}, 4, 30, Extent{-109, 0, 1506, 3226}},
		{"fractional", []tracker{page, {L: -0.5, R: 1404, B: 1872.2}}, 4, 30, Extent{-31, 0, 1404, 1903}},
		{"clamped", []tracker{page, {L: -3e38, T: -3e38, R: 3e38, B: 3e38}}, 4, 30, Extent{-2106, -2808, 3510, 4680}},
		{"mul 1", []tracker{page, {L: -100, B: 5000}}, 1, 30, Extent{0, 0, 1404, 1872}},
		{"pad 48", []tracker{page, {L: 0, T: 0, R: 1404, B: 4112}}, 4, 48, Extent{0, 0, 1404, 4160}},
		{"non-finite ignored", []tracker{page, {L: math.NaN(), T: math.Inf(-1), R: math.Inf(1), B: math.NaN()}}, 4, 30, Extent{0, 0, 1404, 1872}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeExtent(tt.ts, 1404, 1872, tt.mul, tt.pad)
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if w, h := got.R-got.L, got.B-got.T; w > tt.mul*1404 || h > tt.mul*1872 {
				t.Fatalf("extent %vx%v exceeds %v x page", w, h, tt.mul)
			}
		})
	}
}

func TestPlanFrame(t *testing.T) {
	p, err := planFrame(Extent{-109, 0, 1506, 3226}, 0.5, 2, DefaultMaxPixels)
	if err != nil {
		t.Fatal(err)
	}
	if p.x != -109 || p.fw != 1615 || p.fh != 3226 || p.outW != 808 || p.outH != 1613 {
		t.Fatalf("plan %+v", p)
	}
	if _, err := planFrame(Extent{0, 0, 1404, 1872}, 4, 2, DefaultMaxPixels); err == nil {
		t.Fatal("expected pixel budget error for 11232x14976")
	}
	if _, err := planFrame(Extent{0, 0, 1404, 1872}, 0.0001, 1, DefaultMaxPixels); err == nil {
		t.Fatal("expected empty frame error")
	}
}

func TestDownsamplePremultiplied(t *testing.T) {
	// 2x2 block: one opaque blue pixel, three transparent white (the
	// library's background). Straight averaging would give a light blue;
	// premultiplied keeps pure blue at 1/4 alpha.
	src := []byte{
		0, 0, 255, 255, 255, 255, 255, 0,
		255, 255, 255, 0, 255, 255, 255, 0,
	}
	img, ok := downsample(src, 1, 1, 2)
	if !ok {
		t.Fatal("reported empty")
	}
	if got := img.Pix; !bytes.Equal(got, []byte{0, 0, 255, 64}) {
		t.Fatalf("pixel %v, want [0 0 255 64]", got)
	}
	empty := bytes.Repeat([]byte{255, 255, 255, 0}, 16)
	img, ok = downsample(empty, 2, 2, 2)
	if ok || !bytes.Equal(img.Pix, make([]byte, 16)) {
		t.Fatalf("transparent input: ok=%v pix=%v", ok, img.Pix)
	}
	img, ok = downsample([]byte{1, 2, 3, 4}, 1, 1, 1)
	if !ok || !bytes.Equal(img.Pix, []byte{1, 2, 3, 4}) || img.Rect != image.Rect(0, 0, 1, 1) {
		t.Fatalf("ss=1 copy: %v %v", ok, img.Pix)
	}
}
