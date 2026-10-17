package rmlines

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// Wire protocol between the client and a worker, over the worker's stdin and
// stdout. Every message is a frame: a 4-byte big-endian length followed by
// that many bytes.
//
//	worker -> client  hello frame (JSON helloMsg) once at start-up
//	client -> worker  request frame (JSON request)
//	worker -> client  response frame (JSON response)
//	                  if response.Error == "": ink frame (InkW*InkH*4 bytes NRGBA)
//	                  and highlights frame (same size, or empty when !HasHighlights)
//
// One request is in flight per worker. Any framing or size violation makes
// the client kill the worker.

const (
	protocolVersion = 1
	helloMagic      = "rmlines-render-worker"
	maxHeaderFrame  = 1 << 20 // JSON frames
	maxLogBytes     = 16 << 10
	// WorkerArg is the os.Args[1] value that makes the server binary run WorkerMain.
	WorkerArg = "render-worker"
	// debugEnv enables the debug ops (abort, sleep, libaa) in a worker. Tests only.
	debugEnv = "RMLINES_WORKER_DEBUG"
)

type helloMsg struct {
	Hello   string `json:"hello"`
	Version int    `json:"version"`
}

type wireOptions struct {
	Landscape    bool              `json:"landscape"`
	Template     string            `json:"template"`
	Scale        float64           `json:"scale"`
	Supersample  int               `json:"supersample"`
	Images       map[string]string `json:"images"`
	ImagesOn     bool              `json:"imagesOn"`
	MaxExtentMul float64           `json:"maxExtentMul"`
	Pad          float64           `json:"pad"`
	MaxPixels    int               `json:"maxPixels"`
}

type request struct {
	Path    string      `json:"path"`
	Options wireOptions `json:"options"`
	// Debug is honoured only when the worker runs with RMLINES_WORKER_DEBUG=1:
	// "abort" calls abort(), "sleep" blocks forever, "libaa" renders with the
	// library's own antialiasing instead of supersampling, "hideall" treats
	// every layer as hidden.
	Debug string `json:"debug,omitempty"`
}

type response struct {
	PageW         int     `json:"pageW"`
	PageH         int     `json:"pageH"`
	Extent        Extent  `json:"extent"`
	InkW          int     `json:"inkW"`
	InkH          int     `json:"inkH"`
	HasHighlights bool    `json:"hasHighlights"`
	Error         string  `json:"error,omitempty"`
	Log           string  `json:"log,omitempty"`
	Millis        float64 `json:"ms"`
}

func writeFrame(w io.Writer, b []byte) error {
	var hdr [4]byte
	if len(b) > math.MaxUint32 {
		return errors.New("frame too large")
	}
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func writeJSONFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFrame(w, b)
}

// readFrame reads one frame of at most max bytes. When buf has enough
// capacity it is reused.
func readFrame(r io.Reader, max int, buf []byte) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if n > max {
		return nil, fmt.Errorf("frame of %d bytes exceeds limit %d", n, max)
	}
	if cap(buf) >= n {
		buf = buf[:n]
	} else {
		buf = make([]byte, n)
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func readJSONFrame(r io.Reader, v any) error {
	b, err := readFrame(r, maxHeaderFrame, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// normalize applies defaults and validates the options. It runs in the client
// (so bad options never reach a worker) and again in the worker.
func (o wireOptions) normalize() (wireOptions, error) {
	if o.Scale == 0 {
		o.Scale = DefaultScale
	}
	if o.Supersample == 0 {
		o.Supersample = DefaultSupersample
	}
	if o.MaxExtentMul == 0 {
		o.MaxExtentMul = DefaultMaxExtentMul
	}
	if o.MaxPixels == 0 {
		o.MaxPixels = DefaultMaxPixels
	}
	if o.Pad == 0 {
		o.Pad = DefaultPad
	}
	switch {
	case math.IsNaN(o.Scale) || o.Scale <= 0 || o.Scale > 16:
		return o, fmt.Errorf("scale %v out of range (0, 16]", o.Scale)
	case o.Supersample < 1 || o.Supersample > 8:
		return o, fmt.Errorf("supersample %d out of range [1, 8]", o.Supersample)
	case math.IsNaN(o.MaxExtentMul) || o.MaxExtentMul < 1 || o.MaxExtentMul > 64:
		return o, fmt.Errorf("max extent multiple %v out of range [1, 64]", o.MaxExtentMul)
	case math.IsNaN(o.Pad) || o.Pad < 0 || o.Pad > 1000:
		return o, fmt.Errorf("pad %v out of range [0, 1000]", o.Pad)
	case o.MaxPixels < 0:
		return o, fmt.Errorf("negative pixel budget %d", o.MaxPixels)
	case len(o.Template) > 256:
		return o, errors.New("template name too long")
	}
	return o, nil
}

func toWire(opts Options, maxPixels int) wireOptions {
	return wireOptions{
		Landscape:    opts.Landscape,
		Template:     opts.Template,
		Scale:        opts.Scale,
		Supersample:  opts.Supersample,
		Images:       opts.Images,
		ImagesOn:     opts.Images != nil,
		MaxExtentMul: opts.MaxExtentMul,
		Pad:          opts.Pad,
		MaxPixels:    maxPixels,
	}
}
