package inkpdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"runtime"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// MaxPrepareWorkers bounds the parallelism of PrepareImages. Image encoding
// is CPU bound (Flate); a server should not use every core for one export.
const MaxPrepareWorkers = 4

// compressionLevel is the zlib level for image streams. See the benchmark
// notes in the package tests for the trade-off against zlib.BestSpeed.
const compressionLevel = zlib.DefaultCompression

// Prepared is an image already converted to compressed PDF image streams:
// DeviceRGB (or DeviceGray when every visible pixel is grey) plus an 8-bit
// /SMask when any pixel is not opaque. It holds no decoded pixels.
//
// PrepareImage is pure and safe for concurrent use; only inserting the result
// into a Document (StampPrepared, AddPreparedPage) touches the document.
// Each insertion adds a new copy of the image to the document.
type Prepared struct {
	w, h  int
	gray  bool
	color []byte // zlib stream
	alpha []byte // zlib stream; nil when fully opaque
}

// Width and Height return the image size in pixels.
func (p *Prepared) Width() int  { return p.w }
func (p *Prepared) Height() int { return p.h }

// EncodedSize returns the size of the compressed streams in bytes.
func (p *Prepared) EncodedSize() int { return len(p.color) + len(p.alpha) }

var zlibPool = sync.Pool{New: func() any {
	w, _ := zlib.NewWriterLevel(nil, compressionLevel)
	return w
}}

type zstream struct {
	buf bytes.Buffer
	zw  *zlib.Writer
}

func newZStream(sizeHint int) *zstream {
	z := &zstream{zw: zlibPool.Get().(*zlib.Writer)}
	z.buf.Grow(sizeHint)
	z.zw.Reset(&z.buf)
	return z
}

func (z *zstream) finish() ([]byte, error) {
	err := z.zw.Close()
	zlibPool.Put(z.zw)
	if err != nil {
		return nil, err
	}
	return z.buf.Bytes(), nil
}

// PrepareImage encodes img (straight alpha, row 0 at the top) for insertion.
// Rows are streamed into the compressor, so no uncompressed copy of the
// image is held.
func PrepareImage(img *image.NRGBA) (*Prepared, error) {
	if img == nil {
		return nil, fmt.Errorf("inkpdf: nil image")
	}
	b := img.Rect
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("inkpdf: empty image %v", b)
	}
	row := func(y int) []byte {
		o := img.PixOffset(b.Min.X, b.Min.Y+y)
		return img.Pix[o : o+w*4]
	}

	// Pass 1: classify. Colour under fully transparent pixels does not count.
	gray, opaque := true, true
	for y := 0; y < h && (gray || opaque); y++ {
		r := row(y)
		for x := 0; x < len(r); x += 4 {
			a := r[x+3]
			if a != 255 {
				opaque = false
			}
			if a != 0 && (r[x] != r[x+1] || r[x] != r[x+2]) {
				gray = false
			}
		}
	}

	// Pass 2: stream rows into the compressors.
	comps := 3
	if gray {
		comps = 1
	}
	cz := newZStream(w * h * comps / 16)
	var az *zstream
	if !opaque {
		az = newZStream(w * h / 32)
	}
	cbuf := make([]byte, w*comps)
	var abuf []byte
	if az != nil {
		abuf = make([]byte, w)
	}
	var werr error
	for y := 0; y < h; y++ {
		r := row(y)
		for x, i := 0, 0; x < w; x, i = x+1, i+4 {
			if gray {
				cbuf[x] = r[i]
			} else {
				cbuf[3*x], cbuf[3*x+1], cbuf[3*x+2] = r[i], r[i+1], r[i+2]
			}
			if abuf != nil {
				abuf[x] = r[i+3]
			}
		}
		if _, err := cz.zw.Write(cbuf); err != nil && werr == nil {
			werr = err
		}
		if az != nil {
			if _, err := az.zw.Write(abuf); err != nil && werr == nil {
				werr = err
			}
		}
	}
	p := &Prepared{w: w, h: h, gray: gray}
	var err error
	if p.color, err = cz.finish(); err != nil && werr == nil {
		werr = err
	}
	if az != nil {
		if p.alpha, err = az.finish(); err != nil && werr == nil {
			werr = err
		}
	}
	if werr != nil {
		return nil, fmt.Errorf("inkpdf: compress image: %w", werr)
	}
	return p, nil
}

// PrepareImages runs PrepareImage over imgs with at most workers goroutines
// (clamped to 1..MaxPrepareWorkers; <= 0 means min(MaxPrepareWorkers,
// GOMAXPROCS)). The result has the same order as imgs. nil entries yield nil.
func PrepareImages(imgs []*image.NRGBA, workers int) ([]*Prepared, error) {
	if workers <= 0 {
		workers = min(MaxPrepareWorkers, runtime.GOMAXPROCS(0))
	}
	workers = max(1, min(workers, MaxPrepareWorkers, len(imgs)))
	out := make([]*Prepared, len(imgs))
	errs := make([]error, len(imgs))
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if imgs[i] != nil {
					out[i], errs[i] = PrepareImage(imgs[i])
				}
			}
		}()
	}
	for i := range imgs {
		next <- i
	}
	close(next)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i, err)
		}
	}
	return out, nil
}

func flateStream(d types.Dict, raw []byte) types.StreamDict {
	l := int64(len(raw))
	d["Length"] = types.Integer(l)
	d["Filter"] = types.Name("FlateDecode")
	return types.StreamDict{
		Dict:           d,
		StreamLength:   &l,
		FilterPipeline: []types.PDFFilter{{Name: "FlateDecode"}},
		Raw:            raw,
		// Content stays nil: the decoded pixels are never kept in the xref.
	}
}

// insertPrepared adds p to the document as an image XObject (+ SMask).
func (d *Document) insertPrepared(p *Prepared) (*types.IndirectRef, error) {
	cs := types.Name("DeviceRGB")
	if p.gray {
		cs = "DeviceGray"
	}
	img := types.Dict{
		"Type": types.Name("XObject"), "Subtype": types.Name("Image"),
		"Width": types.Integer(p.w), "Height": types.Integer(p.h),
		"BitsPerComponent": types.Integer(8), "ColorSpace": cs,
	}
	if p.alpha != nil {
		sm, err := d.ctx.IndRefForNewObject(flateStream(types.Dict{
			"Type": types.Name("XObject"), "Subtype": types.Name("Image"),
			"Width": types.Integer(p.w), "Height": types.Integer(p.h),
			"BitsPerComponent": types.Integer(8), "ColorSpace": types.Name("DeviceGray"),
		}, p.alpha))
		if err != nil {
			return nil, fmt.Errorf("inkpdf: smask: %w", err)
		}
		img["SMask"] = *sm
	}
	ref, err := d.ctx.IndRefForNewObject(flateStream(img, p.color))
	if err != nil {
		return nil, fmt.Errorf("inkpdf: image: %w", err)
	}
	return ref, nil
}
