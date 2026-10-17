package inkpdf

import (
	"bytes"
	"fmt"
	"image"
	"strings"
	"sync"
	"testing"
)

// inkImage returns a sparse ink-like page: 40 thin dark strokes on
// transparent, different for every seed.
func inkImage(w, h, seed int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < 40; i++ {
		y0 := (seed*97 + i*53) % h
		for x := 0; x < w; x++ {
			y := (y0 + x/(3+i%4)) % h
			for t := 0; t < 3; t++ {
				o := img.PixOffset(x, (y+t)%h)
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(seed), 0, 64, 255
			}
		}
	}
	return img
}

var (
	benchOnce sync.Once
	benchImgs []*image.NRGBA
	benchBG   []byte
)

const benchPages = 200

func benchSetup() {
	benchOnce.Do(func() {
		benchImgs = make([]*image.NRGBA, benchPages)
		var wg sync.WaitGroup
		for i := range benchImgs {
			wg.Add(1)
			go func() { defer wg.Done(); benchImgs[i] = inkImage(1404, 1872, i) }()
		}
		wg.Wait()
		// Letter pages with ~8 KB of vector content each.
		var extra strings.Builder
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&extra, " 0 0 0 RG %d %d m %d %d l S", i, i*2%792, 612-i, (792-i*3%792+792)%792)
		}
		specs := make([]pageSpec, benchPages)
		for i := range specs {
			specs[i] = pageSpec{MediaBox: [4]float64{0, 0, 612, 792}, Extra: extra.String()}
		}
		benchBG = backgroundPDF(specs, "")
	})
}

// Reference numbers (Apple M5 Max, Go 1.26.2, pdfcpu v0.16.2, -benchtime 3x):
//
//	Stamp200     zlib default: 1.32 s/op, 192 MB alloc/op, 137k allocs/op, 17.9 MB output
//	Stamp200     zlib BestSpeed: 0.62 s/op, 219 MB alloc/op, 32.3 MB output
//	Notebook200  zlib default: 1.32 s/op, 174 MB alloc/op, 52k allocs/op, 15.5 MB output
//	PrepareImage zlib default: 25 ms/op (BestSpeed 12 ms), 0.6 MB alloc/op
//
// BenchmarkStamp200 stamps 200 distinct 1404x1872 NRGBA images onto a
// 200-page background: parallel PrepareImages (MaxPrepareWorkers), then
// sequential StampPrepared, Write.
func BenchmarkStamp200(b *testing.B) {
	benchSetup()
	b.ReportAllocs()
	b.ResetTimer()
	var size int
	for b.Loop() {
		d, err := Open(bytes.NewReader(benchBG), OpenOptions{})
		if err != nil {
			b.Fatal(err)
		}
		preps, err := PrepareImages(benchImgs, MaxPrepareWorkers)
		if err != nil {
			b.Fatal(err)
		}
		for i, p := range preps {
			if err := d.StampPrepared(i+1, p, Rect{0, 0, 612, 792}, BlendNormal); err != nil {
				b.Fatal(err)
			}
		}
		var out bytes.Buffer
		if err := d.Write(&out); err != nil {
			b.Fatal(err)
		}
		size = out.Len()
	}
	b.ReportMetric(float64(size)/1e6, "MB-out")
}

// BenchmarkNotebook200 builds a 200-page image notebook.
func BenchmarkNotebook200(b *testing.B) {
	benchSetup()
	b.ReportAllocs()
	b.ResetTimer()
	var size int
	for b.Loop() {
		d, err := NewDocument()
		if err != nil {
			b.Fatal(err)
		}
		preps, err := PrepareImages(benchImgs, MaxPrepareWorkers)
		if err != nil {
			b.Fatal(err)
		}
		for _, p := range preps {
			if _, err := d.AddPreparedPage(p, rmW, rmH); err != nil {
				b.Fatal(err)
			}
		}
		var out bytes.Buffer
		if err := d.Write(&out); err != nil {
			b.Fatal(err)
		}
		size = out.Len()
	}
	b.ReportMetric(float64(size)/1e6, "MB-out")
}

// BenchmarkPrepareImage encodes one 1404x1872 ink page on one goroutine.
func BenchmarkPrepareImage(b *testing.B) {
	benchSetup()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := PrepareImage(benchImgs[7]); err != nil {
			b.Fatal(err)
		}
	}
}
