package rmlines

import (
	"fmt"
	"image"
	"math"
)

// tracker is one getSizeTracker result, in page units.
type tracker struct {
	L  float64 `json:"l"`
	R  float64 `json:"r"`
	T  float64 `json:"t"`
	B  float64 `json:"b"`
	FW float64 `json:"fw"`
	FH float64 `json:"fh"`
}

// computeExtent unions the trackers into an integral rectangle that contains
// the page [0,w]x[0,h], pads the sides where ink leaves the page, and clamps
// the growth so the result is at most mul times the page in each dimension.
// pad is added on each side where ink leaves the page: the library's size
// trackers follow stroke points and ignore stroke width.
// Non-finite tracker values (corrupt files) are ignored.
func computeExtent(ts []tracker, w, h int, mul, pad float64) Extent {
	l, t, r, b := 0.0, 0.0, float64(w), float64(h)
	for _, tr := range ts {
		if finite(tr.L) {
			l = math.Min(l, tr.L)
		}
		if finite(tr.T) {
			t = math.Min(t, tr.T)
		}
		if finite(tr.R) {
			r = math.Max(r, tr.R)
		}
		if finite(tr.B) {
			b = math.Max(b, tr.B)
		}
	}
	el, er := grow(-l, pad), grow(r-float64(w), pad)
	et, eb := grow(-t, pad), grow(b-float64(h), pad)
	el, er = clampGrowth(el, er, (mul-1)*float64(w))
	et, eb = clampGrowth(et, eb, (mul-1)*float64(h))
	return Extent{L: 0 - el, T: 0 - et, R: float64(w) + er, B: float64(h) + eb}
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// grow turns an overhang beyond the page into a padded integral growth.
func grow(over, pad float64) float64 {
	if over <= 0 {
		return 0
	}
	return math.Ceil(over + pad)
}

// clampGrowth scales the growth on both sides down so that a+b <= max.
func clampGrowth(a, b, max float64) (float64, float64) {
	if a+b <= max {
		return a, b
	}
	if max <= 0 {
		return 0, 0
	}
	f := max / (a + b)
	return math.Floor(a * f), math.Floor(b * f)
}

// framePlan is the getFrame call for one page.
type framePlan struct {
	x, y, fw, fh int // viewport in page units
	outW, outH   int // final image size
	ss           int // supersampling factor; the frame is outW*ss x outH*ss
}

// planFrame plans the frame for extent e. The final image may have at most
// maxOut pixels and the supersampled frame at most maxPixels.
func planFrame(e Extent, scale float64, ss, maxPixels, maxOut int) (framePlan, error) {
	p := framePlan{
		x: int(e.L), y: int(e.T),
		fw: int(e.R - e.L), fh: int(e.B - e.T),
		ss: ss,
	}
	p.outW = int(math.Round(float64(p.fw) * scale))
	p.outH = int(math.Round(float64(p.fh) * scale))
	if p.fw <= 0 || p.fh <= 0 || p.outW <= 0 || p.outH <= 0 {
		return p, fmt.Errorf("empty frame %dx%d at scale %v", p.fw, p.fh, scale)
	}
	if float64(p.outW)*float64(p.outH) > float64(maxOut) {
		return p, fmt.Errorf("image %dx%d exceeds the output pixel budget of %d", p.outW, p.outH, maxOut)
	}
	fw, fh := float64(p.outW)*float64(ss), float64(p.outH)*float64(ss)
	if fw*fh > float64(maxPixels) || fw > math.MaxInt32/8 || fh > math.MaxInt32/8 {
		return p, fmt.Errorf("frame %.0fx%.0f exceeds the pixel budget of %d", fw, fh, maxPixels)
	}
	return p, nil
}

// downsample box-filters an NRGBA frame of (w*ss)x(h*ss) pixels into a new
// w x h image. Colours are averaged with premultiplied alpha, so ink edges keep
// the ink colour instead of picking up the transparent-white background.
// It reports whether any output pixel has non-zero alpha.
func downsample(src []byte, w, h, ss int) (*image.NRGBA, bool) {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	nonEmpty := false
	if ss == 1 {
		copy(dst.Pix, src)
		for i := 3; i < len(dst.Pix); i += 4 {
			if dst.Pix[i] != 0 {
				nonEmpty = true
				break
			}
		}
		return dst, nonEmpty
	}
	sw := w * ss
	n := uint32(ss * ss)
	half := n / 2
	for y := 0; y < h; y++ {
		drow := dst.Pix[y*dst.Stride:]
		for x := 0; x < w; x++ {
			var r, g, b, a uint32
			for sy := 0; sy < ss; sy++ {
				i := ((y*ss+sy)*sw + x*ss) * 4
				for sx := 0; sx < ss; sx++ {
					pa := uint32(src[i+3])
					if pa != 0 {
						r += uint32(src[i]) * pa
						g += uint32(src[i+1]) * pa
						b += uint32(src[i+2]) * pa
						a += pa
					}
					i += 4
				}
			}
			if a == 0 {
				continue
			}
			nonEmpty = true
			o := x * 4
			// r/a is the straight colour (premultiplied sum / alpha sum).
			drow[o] = uint8((r + a/2) / a)
			drow[o+1] = uint8((g + a/2) / a)
			drow[o+2] = uint8((b + a/2) / a)
			drow[o+3] = uint8((a + half) / n)
			if drow[o+3] == 0 {
				drow[o], drow[o+1], drow[o+2] = 0, 0, 0
			}
		}
	}
	return dst, nonEmpty
}
