//go:build cgo && rmlines

package rmlines

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -lrm_lines
#cgo rmlines_static,darwin LDFLAGS: -lharfbuzz -lfreetype -lsimdutf -lc++ -framework CoreText -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices
#cgo rmlines_static,linux LDFLAGS: -lharfbuzz -lfreetype -lsimdutf -l:libstdc++.a -lm -static-libgcc
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include "rm_lines_c.h"

// Each wrapper copies the thread_local result inside the same C call, so a
// goroutine moving to another OS thread between calls can never read a
// buffer that another call overwrote. NULL means failure (the library
// returns ""). The caller frees the copy.
static inline char *rml_dup(const char *s) { return (s && *s) ? strdup(s) : NULL; }
static inline char *rml_buildTree(const char *p) { return rml_dup(buildTree(p)); }
static inline char *rml_makeRenderer(const char *t, bool l) { return rml_dup(makeRenderer(t, RML_PAGE_NOTEBOOK, l)); }
static inline char *rml_getLayers(const char *r) { return rml_dup(getLayers(r)); }
static inline char *rml_getSizeTracker(const char *r, const char *l) { return rml_dup(getSizeTracker(r, l)); }
static inline char *rml_getImageInfo(const char *t) { return rml_dup(getImageInfo(t)); }

extern void rmlGoLog(int level, char *msg);
static inline void rml_log_info(const char *m) { rmlGoLog(0, (char *)m); }
static inline void rml_log_debug(const char *m) { rmlGoLog(1, (char *)m); }
static inline void rml_log_error(const char *m) { rmlGoLog(2, (char *)m); }
static inline void rml_hook_loggers(void) {
	setLogger(rml_log_info);
	setErrorLogger(rml_log_error);
	setDebugLogger(rml_log_debug);
}

// Move the protocol off fd 1: anything the C++ side prints to stdout then
// lands on stderr instead of corrupting the frames. Returns the new protocol
// fd, or -1.
static inline int rml_protect_stdout(void) {
	int fd = dup(1);
	if (fd < 0) return -1;
	if (dup2(2, 1) < 0) { close(fd); return -1; }
	return fd;
}

static inline void rml_abort(void) { abort(); }
*/
import "C"

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // image.DecodeConfig for addImage validation
	_ "image/png"
	"io"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

// Available reports whether the v6 renderer is compiled in.
func Available() bool { return true }

// New starts a pool of render workers (lazily, on first use).
func New(cfg Config) (Renderer, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	return c, nil
}

const (
	v6Header     = "reMarkable .lines file, version=6"
	maxFileSize  = 256 << 20
	maxPageUnits = 20000
	maxImageDim  = 16384
	textLayerID  = "7:1" // TEXT_LAYER, renderer.h
)

// libMu serialises every library call: text rendering shares a FreeType
// singleton and crashes when two pages render concurrently.
var libMu sync.Mutex

// WorkerMain runs the render worker loop: it reads requests from stdin and
// writes responses to stdout until stdin is closed. cmd/rmfakecloud calls it
// when os.Args[1] == "render-worker". It returns the process exit code.
func WorkerMain(stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if f, ok := stdout.(*os.File); ok && f.Fd() == 1 {
		if fd := C.rml_protect_stdout(); fd >= 0 {
			stdout = os.NewFile(uintptr(fd), "rmlines-protocol")
		}
	}
	C.rml_hook_loggers()
	debug := os.Getenv(debugEnv) == "1"

	in := bufio.NewReader(stdin)
	out := bufio.NewWriterSize(stdout, 1<<20)
	if err := writeJSONFrame(out, helloMsg{Hello: helloMagic, Version: protocolVersion}); err != nil || out.Flush() != nil {
		fmt.Fprintln(stderr, "rmlines worker: cannot write hello:", err)
		return 1
	}
	for {
		var req request
		if err := readJSONFrame(in, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			fmt.Fprintln(stderr, "rmlines worker: bad request:", err)
			return 1
		}
		resp, ink, hl := serve(req, debug)
		err := writeJSONFrame(out, resp)
		if err == nil && resp.Error == "" {
			err = writeFrame(out, ink)
			if err == nil {
				err = writeFrame(out, hl)
			}
		}
		if err == nil {
			err = out.Flush()
		}
		if err != nil {
			fmt.Fprintln(stderr, "rmlines worker: cannot write response:", err)
			return 1
		}
	}
}

// serve handles one request. Failures come back in resp.Error.
func serve(req request, debug bool) (resp response, ink, hl []byte) {
	libMu.Lock()
	defer libMu.Unlock()
	start := time.Now()
	takeLog()
	defer func() {
		if r := recover(); r != nil {
			resp = response{Error: fmt.Sprintf("internal error: %v", r)}
			ink, hl = nil, nil
		}
		resp.Log = takeLog()
		resp.Millis = float64(time.Since(start).Microseconds()) / 1000
	}()

	var dbg debugFlags
	if debug {
		switch req.Debug {
		case "abort":
			C.rml_abort()
		case "sleep":
			select {}
		case "libaa":
			dbg.libAA = true
		case "hideall":
			dbg.hideAll = true
		}
	}

	pg, err := renderPage(req, dbg)
	if err != nil {
		return response{Error: err.Error()}, nil, nil
	}
	resp = response{
		PageW: pg.pageW, PageH: pg.pageH, Extent: pg.extent,
		InkW: pg.ink.Rect.Dx(), InkH: pg.ink.Rect.Dy(),
	}
	if pg.hl != nil {
		resp.HasHighlights = true
		hl = pg.hl.Pix
	}
	return resp, pg.ink.Pix, hl
}

type page struct {
	pageW, pageH int
	extent       Extent
	ink, hl      *image.NRGBA
}

func cstr(s string) (*C.char, func()) {
	c := C.CString(s)
	return c, func() { C.free(unsafe.Pointer(c)) }
}

// gostr takes ownership of a string returned by an rml_ wrapper.
func gostr(p *C.char) string {
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

// debugFlags are test-only render variants (see request.Debug).
type debugFlags struct {
	libAA   bool // single pass with the library's own antialiasing, no supersampling
	hideAll bool // treat every layer as hidden
}

func renderPage(req request, dbg debugFlags) (*page, error) {
	libAA := dbg.libAA
	opts, err := req.Options.normalize()
	if err != nil {
		return nil, err
	}
	if err := checkRMFile(req.Path); err != nil {
		return nil, err
	}

	cpath, free := cstr(req.Path)
	defer free()
	tree := C.rml_buildTree(cpath)
	if tree == nil {
		return nil, errors.New("buildTree failed (corrupt or unsupported .rm file)")
	}
	defer func() {
		C.destroyTree(tree)
		C.free(unsafe.Pointer(tree))
	}()
	// tree is exactly what buildTree returned, the only id makeRenderer may see.
	rend := C.rml_makeRenderer(tree, C.bool(opts.Landscape))
	if rend == nil {
		return nil, errors.New("makeRenderer failed")
	}
	defer func() { // runs before destroyTree: the renderer points into the tree
		C.destroyRenderer(rend)
		C.free(unsafe.Pointer(rend))
	}()

	layers, err := getLayers(rend)
	if err != nil {
		return nil, err
	}
	// getSizeTracker aborts on unknown ids: only the text layer and the ids
	// getLayers returned are ever passed.
	ids := []string{textLayerID}
	for _, l := range layers {
		ids = append(ids, l.GroupID)
	}
	trackers := make([]tracker, 0, len(ids))
	for _, id := range ids {
		cid, free := cstr(id)
		js := gostr(C.rml_getSizeTracker(rend, cid))
		free()
		var t tracker
		if err := json.Unmarshal([]byte(js), &t); err != nil {
			return nil, fmt.Errorf("size tracker %s: %v", id, err)
		}
		trackers = append(trackers, t)
	}
	fw, fh := trackers[0].FW, trackers[0].FH
	if !finite(fw) || !finite(fh) || fw < 1 || fh < 1 || fw > maxPageUnits || fh > maxPageUnits {
		return nil, fmt.Errorf("implausible page size %vx%v", fw, fh)
	}
	pg := &page{pageW: int(fw + 0.5), pageH: int(fh + 0.5)}
	pg.extent = computeExtent(trackers, pg.pageW, pg.pageH, opts.MaxExtentMul, opts.Pad)
	ss := opts.Supersample
	if libAA {
		ss = 1
	}
	plan, err := planFrame(pg.extent, opts.Scale, ss, opts.MaxPixels, opts.MaxOutPixels)
	if err != nil {
		return nil, err
	}

	cfg := C.getConfig(rend)
	if cfg == nil {
		return nil, errors.New("getConfig returned NULL")
	}
	if cfg.configVersion != C.RML_CONFIG_VERSION {
		return nil, fmt.Errorf("RendererConfig version %d, built for %d", cfg.configVersion, C.RML_CONFIG_VERSION)
	}
	if dbg.hideAll {
		for i := range layers {
			layers[i].Visible = false
		}
	}
	disableHiddenLayers(cfg, layers)

	images := false
	if opts.ImagesOn {
		images = addImages(tree, rend, opts.Images)
	}

	if opts.Template != "" {
		ct, free := cstr(opts.Template)
		C.setTemplate(rend, ct)
		free()
	}

	frameW, frameH := plan.outW*plan.ss, plan.outH*plan.ss
	buf := make([]byte, frameW*frameH*4)
	frame := func() {
		// getFrame aborts unless dataSize >= width*height*4: buf is exactly that.
		if len(buf) != frameW*frameH*4 {
			panic("frame buffer size mismatch")
		}
		C.getFrame(rend, (*C.uint32_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)),
			C.int(plan.x), C.int(plan.y), C.int(plan.fw), C.int(plan.fh),
			C.int(frameW), C.int(frameH), C.bool(libAA))
	}

	// Pass 1: everything except highlighters and glyph highlights.
	setPenList(&cfg.penWhitelist)
	setPenList(&cfg.penBlacklist, C.RML_TOOL_HIGHLIGHTER_1, C.RML_TOOL_HIGHLIGHTER_2)
	cfg.useWhitelist = false
	cfg.enableText = true
	cfg.enableImages = C.bool(images)
	cfg.enableGlyphHighlights = false
	cfg.enableBackdrop = false
	frame()
	pg.ink, _ = downsample(buf, plan.outW, plan.outH, plan.ss)
	if libAA {
		return pg, nil
	}

	// Pass 2: highlighter strokes and glyph highlights only, no text, no
	// images, no template.
	bl, free2 := cstr("Blank")
	C.setTemplate(rend, bl)
	free2()
	setPenList(&cfg.penWhitelist, C.RML_TOOL_HIGHLIGHTER_1, C.RML_TOOL_HIGHLIGHTER_2)
	setPenList(&cfg.penBlacklist)
	cfg.useWhitelist = true
	cfg.enableText = false
	cfg.enableImages = false
	cfg.enableGlyphHighlights = true
	frame()
	if hl, ok := downsample(buf, plan.outW, plan.outH, plan.ss); ok {
		pg.hl = hl
	}
	return pg, nil
}

// checkRMFile rejects anything that is not a plausible v6 file before the
// library sees it.
func checkRMFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if st.Size() > maxFileSize {
		return fmt.Errorf("%s is %d bytes, limit %d", path, st.Size(), maxFileSize)
	}
	hdr := make([]byte, len(v6Header))
	if _, err := io.ReadFull(f, hdr); err != nil || string(hdr) != v6Header {
		return errors.New("not a v6 .rm file")
	}
	return nil
}

type layerInfo struct {
	GroupID string `json:"groupId"`
	Visible bool   `json:"visible"`
	Label   string `json:"label"`
	id      [2]uint64
}

var crdtRe = regexp.MustCompile(`^([0-9]{1,3}):([0-9]{1,20})$`)

func parseCrdt(s string) ([2]uint64, bool) {
	m := crdtRe.FindStringSubmatch(s)
	if m == nil {
		return [2]uint64{}, false
	}
	a, err1 := strconv.ParseUint(m[1], 10, 8)
	b, err2 := strconv.ParseUint(m[2], 10, 64)
	return [2]uint64{a, b}, err1 == nil && err2 == nil
}

func getLayers(rend *C.char) ([]layerInfo, error) {
	js := gostr(C.rml_getLayers(rend))
	if js == "" {
		return nil, nil
	}
	var layers []layerInfo
	if err := json.Unmarshal([]byte(js), &layers); err != nil {
		return nil, fmt.Errorf("getLayers: %v", err)
	}
	for i := range layers {
		id, ok := parseCrdt(layers[i].GroupID)
		if !ok {
			return nil, fmt.Errorf("getLayers: malformed layer id %q", layers[i].GroupID)
		}
		layers[i].id = id
	}
	return layers, nil
}

func disableHiddenLayers(cfg *C.struct_RendererConfig, layers []layerInfo) {
	for i := range cfg.disabledLayers {
		cfg.disabledLayers[i] = C.RmlCrdtId{}
	}
	n := 0
	for _, l := range layers {
		if l.Visible || l.id == ([2]uint64{}) { // {0,0} marks an unused slot
			continue
		}
		if n == len(cfg.disabledLayers) {
			rmlGoLogString(fmt.Sprintf("more than %d hidden layers; %s (%q) stays visible", n, l.GroupID, l.Label))
			continue
		}
		cfg.disabledLayers[n] = C.RmlCrdtId{first: C.uint8_t(l.id[0]), second: C.uint64_t(l.id[1])}
		n++
	}
}

func setPenList(list *[C.RML_PEN_LIST_LEN]C.int8_t, tools ...int) {
	for i := range list {
		list[i] = -1
	}
	for i, t := range tools {
		list[i] = C.int8_t(t)
	}
}

// addImages registers every image the page references. It returns false
// (images disabled) unless all of them resolve to a decodable file: a missing
// texture renders as an opaque magenta box.
func addImages(tree, rend *C.char, files map[string]string) bool {
	js := gostr(C.rml_getImageInfo(tree))
	if js == "" {
		return true // no images on the page
	}
	var info map[string]struct {
		FileName struct {
			Value string `json:"value"`
		} `json:"fileName"`
	}
	if err := json.Unmarshal([]byte(js), &info); err != nil {
		rmlGoLogString("getImageInfo: " + err.Error())
		return false
	}
	paths := map[string]string{}
	for uuid, im := range info {
		p, ok := files[uuid]
		if !ok && im.FileName.Value != "" {
			p, ok = files[im.FileName.Value]
		}
		if !ok {
			rmlGoLogString(fmt.Sprintf("image %s (%s) not supplied; images disabled", uuid, im.FileName.Value))
			return false
		}
		if err := checkImage(p); err != nil {
			rmlGoLogString(fmt.Sprintf("image %s: %v; images disabled", uuid, err))
			return false
		}
		paths[uuid] = p
	}
	for uuid, p := range paths {
		cu, f1 := cstr(uuid)
		cp, f2 := cstr(p)
		C.addImage(rend, cu, cp)
		f1()
		f2()
	}
	return true
}

func checkImage(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return err
	}
	if format != "png" && format != "jpeg" {
		return fmt.Errorf("unsupported image format %s", format)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxImageDim || cfg.Height > maxImageDim {
		return fmt.Errorf("image size %dx%d out of range", cfg.Width, cfg.Height)
	}
	return nil
}

func rmlGoLogString(s string) {
	logMu.Lock()
	defer logMu.Unlock()
	if logBuf.Len() > 0 {
		logBuf.WriteString("\n")
	}
	logBuf.WriteString(s)
}
