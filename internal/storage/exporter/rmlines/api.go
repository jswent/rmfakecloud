// Package rmlines renders reMarkable v6 .rm pages to bitmaps with LIB rMLines
// (https://github.com/RedTTGMoss/librm_lines).
//
// The library is C++ and has several calls that abort the process on bad
// input, so it never runs inside the server: New starts worker subprocesses
// ("<exe> render-worker", see WorkerMain) and talks to them over a pipe. The
// real implementation needs cgo and the build tag "rmlines"; without them the
// package compiles to a stub whose New returns ErrNotAvailable.
package rmlines

import (
	"context"
	"errors"
	"image"
	"time"
)

// ErrNotAvailable is returned by New when the renderer was not compiled in.
var ErrNotAvailable = errors.New("rmlines: v6 renderer not compiled in (build with -tags rmlines and cgo)")

var (
	// ErrWorkerCrashed means the worker process died or broke the protocol
	// while handling the request. The worker is respawned on the next request.
	ErrWorkerCrashed = errors.New("rmlines: render worker crashed")
	// ErrTimeout means the page took longer than Options.Timeout. The worker
	// was killed and is respawned on the next request.
	ErrTimeout = errors.New("rmlines: render timed out")
	// ErrRender means the library (or the argument checks in front of it)
	// rejected the page. The worker is still healthy. The error text carries
	// the library's error log.
	ErrRender = errors.New("rmlines: render failed")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("rmlines: renderer closed")
)

// Defaults applied to zero-valued Options and Config fields.
const (
	DefaultScale        = 1.0
	DefaultSupersample  = 2
	DefaultMaxExtentMul = 4.0
	DefaultPad          = 30.0
	DefaultTimeout      = 30 * time.Second
	DefaultWorkers      = 1
	DefaultIdleTimeout  = 5 * time.Minute
	DefaultMaxPixels    = 64 << 20 // 64 Mpx per supersampled frame
)

// Options control how a single page is rendered.
type Options struct {
	Landscape    bool              // from .content orientation
	Template     string            // passed to setTemplate; unknown names fall back to Blank
	Scale        float64           // output pixels per page unit; default 1.0
	Supersample  int               // render at Scale*Supersample, box-downsample with premultiplied alpha; default 2; 1 disables
	Images       map[string]string // imageRef uuid -> PNG/JPEG file path for addImage; nil => images disabled (no magenta boxes)
	MaxExtentMul float64           // the extent may not exceed this multiple of the page width/height; default 4
	Pad          float64           // page units added on each side where ink leaves the page (trackers ignore stroke width); 0 means DefaultPad (30)
	Timeout      time.Duration     // per page; default 30s
}

// Extent is a rectangle in page units. L and T may be negative and R and B may
// exceed the page size when the ink grows beyond the page.
type Extent struct{ L, T, R, B float64 }

// Result is a rendered page.
type Result struct {
	// PageW, PageH is the page frame in page units: the SceneInfo paper size
	// (1404x1872 when absent; 1620x2160 on Paper Pro), swapped when
	// Options.Landscape is set. Extent is expressed in this frame.
	PageW, PageH int
	// Extent is the rendered region: the union of the library's size
	// trackers, padded by Options.Pad units on every side where ink leaves the page,
	// and clamped by Options.MaxExtentMul. It always contains the page.
	Extent Extent
	// Ink holds everything except highlighter strokes and glyph highlights.
	// Ink.Rect.Dx() == round((Extent.R-Extent.L)*Scale).
	Ink *image.NRGBA
	// Highlights holds highlighter strokes and glyph (text) highlights only,
	// nil when there are none. Same bounds as Ink. These pixels are opaque
	// and meant to be composited with a multiply blend.
	Highlights *image.NRGBA
}

// Renderer renders .rm files.
type Renderer interface {
	RenderFile(ctx context.Context, rmPath string, opts Options) (*Result, error)
	Close() error
}

// Config configures the worker pool behind a Renderer.
type Config struct {
	Workers     int           // number of worker subprocesses, default 1
	WorkerPath  string        // executable to spawn with argument "render-worker"; default os.Executable()
	IdleTimeout time.Duration // kill idle workers after this long, default 5m
	MaxPixels   int           // refuse supersampled frames larger than this many pixels, default 64 Mpx
}
