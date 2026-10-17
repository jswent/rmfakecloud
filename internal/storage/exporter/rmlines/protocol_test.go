package rmlines

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os/exec"
	"testing"
	"time"
)

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	if err := writeJSONFrame(&b, request{Path: "/x.rm", Options: wireOptions{Scale: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(&b, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(&b, nil); err != nil {
		t.Fatal(err)
	}
	var req request
	if err := readJSONFrame(&b, &req); err != nil || req.Path != "/x.rm" || req.Options.Scale != 2 {
		t.Fatalf("request %+v, %v", req, err)
	}
	if _, err := readFrame(bytes.NewReader(b.Bytes()), 2, nil); err == nil {
		t.Fatal("oversized frame accepted")
	}
	f, err := readFrame(&b, 3, nil)
	if err != nil || !bytes.Equal(f, []byte{1, 2, 3}) {
		t.Fatalf("frame %v, %v", f, err)
	}
	if f, err = readFrame(&b, 3, nil); err != nil || len(f) != 0 {
		t.Fatalf("empty frame %v, %v", f, err)
	}
	if _, err := readFrame(bytes.NewReader([]byte{0, 0, 0, 9, 1}), 100, nil); err == nil {
		t.Fatal("truncated frame accepted")
	}
}

func TestNormalize(t *testing.T) {
	o, err := wireOptions{}.normalize()
	if err != nil || o.Scale != 1 || o.Supersample != 2 || o.MaxExtentMul != 4 || o.MaxPixels != DefaultMaxPixels {
		t.Fatalf("defaults %+v, %v", o, err)
	}
	for _, bad := range []wireOptions{{Scale: -1}, {Scale: math.NaN()}, {Supersample: 9}, {Supersample: -1}, {MaxExtentMul: 0.5}} {
		if _, err := bad.normalize(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// A WorkerPath that is not a render worker fails the handshake cleanly.
func TestClientBadWorker(t *testing.T) {
	path, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1)")
	}
	c, err := newClient(Config{WorkerPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.RenderFile(ctx, "../../../../testdata/rm/v6/Layers.rm", Options{})
	if !errors.Is(err, ErrWorkerCrashed) {
		t.Fatalf("err = %v, want ErrWorkerCrashed", err)
	}
	if _, err := c.RenderFile(ctx, "x.rm", Options{Scale: -1}); !errors.Is(err, ErrRender) {
		t.Fatalf("bad options: %v", err)
	}
	c.Close()
	if _, err := c.RenderFile(ctx, "x.rm", Options{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}
