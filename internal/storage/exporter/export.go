// Package exporter renders reMarkable documents to PDF.
//
// Export reads a document through a Source (the .content file, the PDF or
// EPUB payload, and one .rm blob per page), builds the page plan
// (content.BuildPlan), and assembles a PDF with inkpdf:
//
//   - v6 pages (tablet software 3.x) are rasterised by LIB rMLines in a
//     worker subprocess (package rmlines) and placed as images;
//   - v3/v5 pages (software 1.x/2.x) are drawn as vector strokes (v5render);
//   - pages without ink are blank, or just the background page;
//   - on a PDF payload, ink is stamped onto its pages, inserted pages are
//     added, and the pages are put in the document's order at the end.
//
// A page that fails to render is logged and exported blank; the export as a
// whole fails only when the document cannot be read or written. When the
// renderer is not compiled in (builds without the rmlines tag) or disabled,
// v6 pages export as blank pages of the right size where it is known.
package exporter

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ddvk/rmfakecloud/internal/storage/exporter/content"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/inkpdf"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/rmlines"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter/v5render"
	"github.com/juruen/rmapi/encoding/rm"
	log "github.com/sirupsen/logrus"
)

// Config configures an Exporter. Zero values select the defaults.
type Config struct {
	// Disable turns the v6 renderer off: v6 pages export blank.
	Disable bool
	// Workers is the number of render worker processes (default 1).
	Workers int
	// Scale is the number of image pixels per page unit (default 1.0).
	Scale float64
	// Supersample renders at Scale*Supersample and downsamples (default 2).
	Supersample int
	// Timeout bounds the rendering of one page (default 30s).
	Timeout time.Duration
	// PtPerUnit is the size of a page unit in PDF points (default 72/227).
	PtPerUnit float64
	// NoClip keeps ink that grows left of, right of or above a notebook
	// page (the tablet cuts it off). For debugging.
	NoClip bool
	// WorkerPath is the executable started as render worker (default: this
	// executable).
	WorkerPath string
	// TempDir is where .rm files held in memory are written for the
	// renderer (default os.TempDir()).
	TempDir string
}

// Stats summarises one export.
type Stats struct {
	Pages  int // pages in the output
	V6     int // v6 pages rendered
	V5     int // v3/v5 pages drawn
	Blank  int // pages without ink (or only their background)
	Failed int // pages that failed and were exported blank
	// V6Unavailable counts v6 pages exported blank because the renderer is
	// not available.
	V6Unavailable int
}

// Exporter turns documents into PDFs. It is safe for concurrent use.
type Exporter struct {
	cfg      Config
	geo      geometry
	renderer rmlines.Renderer // nil when v6 rendering is unavailable
	noV6     string           // why renderer is nil
	warnOnce sync.Once
}

// New returns an Exporter. It logs whether v6 rendering is available; the
// worker processes start on the first v6 page.
func New(cfg Config) *Exporter {
	if cfg.Workers <= 0 {
		cfg.Workers = rmlines.DefaultWorkers
	}
	if cfg.Scale <= 0 {
		cfg.Scale = rmlines.DefaultScale
	}
	if cfg.Supersample <= 0 {
		cfg.Supersample = rmlines.DefaultSupersample
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = rmlines.DefaultTimeout
	}
	if cfg.PtPerUnit <= 0 {
		cfg.PtPerUnit = DefaultPtPerUnit
	}
	e := &Exporter{
		cfg: cfg,
		geo: geometry{ppu: cfg.PtPerUnit, scale: cfg.Scale, clip: !cfg.NoClip},
	}
	switch {
	case cfg.Disable:
		e.noV6 = "disabled by configuration"
		log.Info("exporter: v6 page rendering is disabled; pages written with tablet software 3.x export blank")
	case !rmlines.Available():
		e.noV6 = "not compiled in"
		log.Info("exporter: this build has no v6 page renderer; pages written with tablet software 3.x export blank. " +
			"Use the container image or build with cgo and -tags rmlines (see docs/usage/rendering.md)")
	default:
		r, err := rmlines.New(rmlines.Config{Workers: cfg.Workers, WorkerPath: cfg.WorkerPath})
		if err != nil {
			e.noV6 = err.Error()
			log.Warnf("exporter: v6 page renderer unavailable: %v", err)
		} else {
			e.renderer = r
			log.Infof("exporter: v6 page renderer enabled (%d worker(s))", cfg.Workers)
		}
	}
	return e
}

// cacheVersion changes when the output of the same input changes.
const cacheVersion = 1

// CacheKey identifies the settings that shape the output, so that a cached
// export made with other settings (or before the renderer was available) is
// not reused. It is short and safe in file names.
func (e *Exporter) CacheKey() string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%d|%t|%v|%d|%v|%t", cacheVersion, e.renderer != nil, e.cfg.Scale, e.cfg.Supersample, e.cfg.PtPerUnit, e.cfg.NoClip)
	return fmt.Sprintf("%08x", h.Sum32())
}

// Close stops the render workers.
func (e *Exporter) Close() error {
	if e.renderer == nil {
		return nil
	}
	return e.renderer.Close()
}

type pageKind int

const (
	kindBlank pageKind = iota
	kindV6
	kindV5
	kindFailed
)

// job is one page on its way from the renderer to the document. The
// producer renders pages in order; images are compressed in parallel; the
// consumer adds them to the document in order.
type job struct {
	no   int // 1-based position in the plan
	page content.PlanPage
	bg   int // background page number, 0 for none
	kind pageKind
	err  error

	// v6
	pageW, pageH int
	ext          rmlines.Extent // what the images cover
	frame        rmlines.Extent // notebook page in page units
	ink, hl      *inkpdf.Prepared
	// v5
	lines *rm.Rm

	done chan struct{}
}

// Export renders src as a PDF to w.
func (e *Exporter) Export(ctx context.Context, src *Source, w io.Writer) (*Stats, error) {
	start := time.Now()
	c, err := content.Parse(src.Content)
	if errors.Is(err, content.ErrNoPageList) {
		log.Warnf("exporter: %v; exporting the payload only", err)
	} else if err != nil {
		return nil, fmt.Errorf("exporter: %w", err)
	}
	if src.Pagedata != nil {
		content.ApplyPagedata(c, src.Pagedata)
	}
	blobs := matchBlobs(c, src.Pages)
	formats := make(map[string]content.Format, len(blobs))
	for id, b := range blobs {
		h, err := b.header(content.HeaderLen)
		if err != nil {
			log.Warnf("exporter: page %s: %v", id, err)
		}
		formats[id] = content.DetectFormat(h)
	}
	plan, err := content.BuildPlan(c, formats)
	if err != nil {
		return nil, fmt.Errorf("exporter: %w", err)
	}

	var doc *inkpdf.Document
	bgPages := 0
	switch {
	case plan.PayloadType == "pdf" && src.Payload != nil:
		doc, err = inkpdf.Open(src.Payload, inkpdf.OpenOptions{Decrypt: true})
		if errors.Is(err, inkpdf.ErrWrongPassword) {
			log.Warnf("exporter: the PDF is password protected; exporting the ink without it")
			doc = nil
		} else if err != nil {
			return nil, fmt.Errorf("exporter: %w", err)
		} else {
			bgPages = doc.PageCount()
		}
	case plan.PayloadType == "epub":
		log.Info("exporter: EPUB document: its pages cannot be reproduced, exporting the ink on blank pages")
	}
	if doc == nil {
		if doc, err = inkpdf.NewDocument(); err != nil {
			return nil, fmt.Errorf("exporter: %w", err)
		}
	}

	st := &Stats{}
	order := make([]int, 0, len(plan.Pages))
	if len(plan.Pages) == 0 && bgPages > 0 {
		for i := 1; i <= bgPages; i++ {
			order = append(order, i)
		}
	} else {
		jobs := make(chan *job, inkpdf.MaxPrepareWorkers)
		pctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.produce(pctx, plan, bgPages, blobs, jobs)
		}()
		a := assembler{e: e, doc: doc, landscape: plan.Landscape, used: map[int]bool{}, st: st}
		for j := range jobs {
			<-j.done
			n := a.apply(j)
			if n > 0 {
				order = append(order, n)
			}
		}
		cancel()
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(order) == 0 {
			// A notebook without pages: an empty PDF is invalid.
			pw, ph := e.geo.nominalSize(deviceW, deviceH, plan.Landscape)
			n, err := doc.AddBlankPage(pw, ph)
			if err != nil {
				return nil, fmt.Errorf("exporter: %w", err)
			}
			order = append(order, n)
		}
	}
	if err := doc.Reorder(order); err != nil {
		return nil, fmt.Errorf("exporter: %w", err)
	}
	if err := doc.Write(w); err != nil {
		return nil, fmt.Errorf("exporter: %w", err)
	}
	st.Pages = len(order)

	if st.V6Unavailable > 0 {
		e.warnOnce.Do(func() {
			log.Warnf("exporter: v6 pages export blank: renderer %s", e.noV6)
		})
	}
	log.Infof("exporter: %d pages (%d v6, %d v5, %d blank, %d v6 without renderer), %d failed, in %v",
		st.Pages, st.V6, st.V5, st.Blank, st.V6Unavailable, st.Failed, time.Since(start).Round(time.Millisecond))
	return st, nil
}

// matchBlobs maps blobs to page ids. Old documents name .rm files by page
// index ("0.rm"); such a blob is matched to the page at that index unless that
// page has a blob under its id.
func matchBlobs(c *content.Content, in map[string]Blob) map[string]Blob {
	known := make(map[string]bool, len(c.Pages))
	for _, p := range c.Pages {
		known[p.ID] = true
	}
	out := make(map[string]Blob, len(in))
	for id, b := range in {
		if known[id] {
			out[id] = b
		}
	}
	for id, b := range in {
		if known[id] {
			continue
		}
		if i, err := strconv.Atoi(id); err == nil && i >= 0 && i < len(c.Pages) {
			if _, taken := out[c.Pages[i].ID]; !taken {
				pid := c.Pages[i].ID
				out[pid] = b
				continue
			}
		}
		out[id] = b // BuildPlan logs and ignores it
	}
	return out
}

// produce renders the pages in plan order and sends them to jobs, which it
// closes. Image compression runs in parallel with the rendering of the next
// pages; jobs' capacity bounds how many pages are in memory.
func (e *Exporter) produce(ctx context.Context, plan *content.Plan, bgPages int, blobs map[string]Blob, jobs chan<- *job) {
	defer close(jobs)
	var tmpDir string
	defer func() {
		if tmpDir != "" {
			os.RemoveAll(tmpDir)
		}
	}()
	sem := make(chan struct{}, inkpdf.MaxPrepareWorkers)

	for i, pp := range plan.Pages {
		j := &job{no: i + 1, page: pp, done: make(chan struct{})}
		if pp.Background > 0 {
			if pp.Background <= bgPages {
				j.bg = pp.Background
			} else if bgPages > 0 {
				log.Infof("exporter: page %d: background page %d is past the end of the PDF (%d pages); adding it as an inserted page",
					j.no, pp.Background, bgPages)
			}
		}
		blob := blobs[pp.ID]
		async := false
		switch {
		case !pp.HasInk:
			j.kind = kindBlank
		case pp.Format == content.FormatV6 && e.renderer == nil:
			j.kind = kindBlank
			j.err = errNoRenderer
		case pp.Format == content.FormatV6:
			path := blob.Path
			if path == "" {
				var err error
				if tmpDir == "" {
					tmpDir, err = os.MkdirTemp(e.cfg.TempDir, "rmexport-")
				}
				if err == nil {
					path = filepath.Join(tmpDir, strconv.Itoa(j.no)+".rm")
					err = os.WriteFile(path, blob.Data, 0600)
				}
				if err != nil {
					j.kind, j.err = kindFailed, err
					break
				}
			}
			log.Debugf("exporter: page %d: v6, template %q (not drawn)", j.no, pp.Template)
			res, err := e.render(ctx, path, plan.Landscape)
			if err != nil {
				j.kind, j.err = kindFailed, err
				break
			}
			j.kind = kindV6
			j.pageW, j.pageH, j.ext = res.PageW, res.PageH, res.Extent
			ink, hl := res.Ink, res.Highlights
			if j.bg == 0 {
				j.frame = e.geo.notebookFrame(res)
				r := e.geo.cropRect(ink.Rect, res.Extent, j.frame)
				ink, hl = crop(ink, r), crop(hl, r)
			}
			async = true
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; close(j.done) }()
				if hasAlpha(ink) {
					if j.ink, j.err = inkpdf.PrepareImage(ink); j.err != nil {
						j.kind = kindFailed
						return
					}
				}
				if hl != nil && hasAlpha(hl) {
					if j.hl, j.err = inkpdf.PrepareImage(hl); j.err != nil {
						j.kind = kindFailed
					}
				}
			}()
		case pp.Format == content.FormatV5 || pp.Format == content.FormatV3:
			data, err := blob.bytes()
			if err == nil {
				j.lines = rm.New()
				err = j.lines.UnmarshalBinary(data)
			}
			if err != nil {
				j.kind, j.err = kindFailed, fmt.Errorf("parse %s .rm: %w", pp.Format, err)
				break
			}
			j.kind = kindV5
		default:
			log.Warnf("exporter: page %d (%s): unrecognised .rm header; exporting it blank", j.no, pp.ID)
			j.kind = kindBlank
		}
		if !async {
			close(j.done)
		}
		select {
		case jobs <- j:
		case <-ctx.Done():
			<-j.done
			return
		}
	}
}

var errNoRenderer = errors.New("v6 renderer not available")

// render renders one v6 page. A page the library rejects is retried once
// without supersampling: that quarters the frame, which is what a page over
// the pixel budget needs.
func (e *Exporter) render(ctx context.Context, path string, landscape bool) (*rmlines.Result, error) {
	opts := rmlines.Options{
		Landscape:    landscape,
		Template:     "", // librm_lines draws only Blank and the P Grid family; templates are not drawn yet
		Scale:        e.cfg.Scale,
		Supersample:  e.cfg.Supersample,
		Images:       nil, // images disabled until image blobs are wired through
		MaxExtentMul: maxGrowth,
		Pad:          tabletPad,
		Timeout:      e.cfg.Timeout,
	}
	res, err := e.renderer.RenderFile(ctx, path, opts)
	if err != nil && errors.Is(err, rmlines.ErrRender) && opts.Supersample > 1 {
		log.Debugf("exporter: %v; retrying without supersampling", err)
		opts.Supersample = 1
		res, err = e.renderer.RenderFile(ctx, path, opts)
	}
	return res, err
}

// hasAlpha reports whether any pixel of img is not fully transparent.
func hasAlpha(img *image.NRGBA) bool {
	b := img.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		o := img.PixOffset(b.Min.X, y)
		row := img.Pix[o : o+b.Dx()*4]
		for i := 3; i < len(row); i += 4 {
			if row[i] != 0 {
				return true
			}
		}
	}
	return false
}

// assembler adds finished jobs to the document, in plan order.
type assembler struct {
	e         *Exporter
	doc       *inkpdf.Document
	landscape bool
	used      map[int]bool // background pages already placed
	st        *Stats
	// last device page seen, for the size of blank pages
	lastW, lastH int
}

// apply adds j to the document and returns its page number there.
func (a *assembler) apply(j *job) int {
	if j.kind == kindV6 {
		a.lastW, a.lastH = j.pageW, j.pageH
	}
	if j.bg > 0 && a.used[j.bg] {
		log.Warnf("exporter: page %d: background page %d is used twice; it is repeated with the ink of both", j.no, j.bg)
	}
	n, err := a.place(j)
	switch {
	case err != nil:
		a.st.Failed++
		log.Errorf("exporter: page %d (%s): %v; exporting it blank", j.no, j.page.ID, err)
		if n == 0 {
			n = a.blank(j)
		}
	case j.kind == kindV6:
		a.st.V6++
	case j.kind == kindV5:
		a.st.V5++
	case errors.Is(j.err, errNoRenderer):
		a.st.V6Unavailable++
	default:
		a.st.Blank++
	}
	if j.bg > 0 {
		a.used[j.bg] = true
	}
	return n
}

// place adds the page. On error it returns the page number if the page was
// already added (a partly stamped page), or 0.
func (a *assembler) place(j *job) (int, error) {
	geo := a.e.geo
	if j.kind == kindFailed {
		return 0, j.err
	}
	switch {
	case j.kind == kindV6 && j.bg > 0:
		vw, _, err := a.doc.PageSize(j.bg)
		if err != nil {
			return 0, err
		}
		r := v6Placement(j.ext, j.pageW, vw, geo.ppu)
		if j.ink != nil {
			if err := a.doc.StampPrepared(j.bg, j.ink, r, inkpdf.BlendNormal); err != nil {
				return j.bg, err
			}
		}
		if j.hl != nil {
			if err := a.doc.StampPrepared(j.bg, j.hl, r, inkpdf.BlendMultiply); err != nil {
				return j.bg, err
			}
		}
		return j.bg, nil
	case j.kind == kindV6:
		w, h := geo.pt(j.frame.R-j.frame.L), geo.pt(j.frame.B-j.frame.T)
		var n int
		var err error
		if j.ink != nil {
			n, err = a.doc.AddPreparedPage(j.ink, w, h)
		} else {
			n, err = a.doc.AddBlankPage(w, h)
		}
		if err != nil {
			return 0, err
		}
		if j.hl != nil {
			if err := a.doc.StampPrepared(n, j.hl, inkpdf.Rect{X: 0, Y: 0, W: w, H: h}, inkpdf.BlendMultiply); err != nil {
				return n, err
			}
		}
		return n, nil
	case j.kind == kindV5:
		n := j.bg
		var vw, vh float64
		var err error
		if n > 0 {
			vw, vh, err = a.doc.PageSize(n)
		} else {
			vw, vh = geo.nominalSize(deviceW, deviceH, a.landscape)
			n, err = a.doc.AddBlankPage(vw, vh)
		}
		if err != nil {
			return 0, err
		}
		out, err := v5render.Render(j.lines, v5render.Options{PageW: vw, PageH: vh, Landscape: a.landscape})
		if err != nil {
			return n, err
		}
		if err := a.doc.StampContent(n, out.Content, out.Resources); err != nil {
			return n, err
		}
		return n, nil
	default:
		if j.bg > 0 {
			return j.bg, nil
		}
		return a.blankPage()
	}
}

// blank adds the page without ink after a failure.
func (a *assembler) blank(j *job) int {
	if j.bg > 0 {
		return j.bg
	}
	n, err := a.blankPage()
	if err != nil {
		log.Errorf("exporter: page %d: %v", j.no, err)
		return 0
	}
	return n
}

// blankPage appends a blank page the size of the last v6 page seen, or of an
// rM2 page.
func (a *assembler) blankPage() (int, error) {
	w, h := a.lastW, a.lastH
	if w == 0 || h == 0 {
		w, h = deviceW, deviceH
	}
	pw, ph := a.e.geo.nominalSize(w, h, a.landscape)
	return a.doc.AddBlankPage(pw, ph)
}
