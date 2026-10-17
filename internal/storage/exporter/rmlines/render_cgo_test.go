//go:build cgo && rmlines

package rmlines

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden PNGs in testdata/golden")

const (
	fixtures    = "../../../../testdata/rm/v6"
	imageDir    = "../../../../testdata/rm/images"
	goldenScale = 0.25
)

func fixture(name string) string { return filepath.Join(fixtures, name) }

// outDir is where full-size renders go for human inspection:
// $RMLINES_RENDER_DIR, or <tmp>/rmlines-renders.
func outDir(t testing.TB) string {
	d := os.Getenv("RMLINES_RENDER_DIR")
	if d == "" {
		d = filepath.Join(os.TempDir(), "rmlines-renders")
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func savePNG(t testing.TB, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// composite flattens a result onto white the way a PDF viewer would show it:
// ink over white, then highlights with a multiply blend.
func composite(res *Result) *image.NRGBA {
	b := res.Ink.Rect
	out := image.NewNRGBA(b)
	draw.Draw(out, b, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(out, b, res.Ink, b.Min, draw.Over)
	if hl := res.Highlights; hl != nil {
		for i := 0; i < len(out.Pix); i += 4 {
			a := uint32(hl.Pix[i+3])
			for c := 0; c < 3; c++ {
				d := uint32(out.Pix[i+c])
				m := d * uint32(hl.Pix[i+c]) / 255
				out.Pix[i+c] = uint8((d*(255-a) + m*a) / 255)
			}
		}
	}
	return out
}

func saveResult(t testing.TB, name string, res *Result) {
	d := outDir(t)
	savePNG(t, filepath.Join(d, name+".ink.png"), res.Ink)
	if res.Highlights != nil {
		savePNG(t, filepath.Join(d, name+".highlights.png"), res.Highlights)
	}
	savePNG(t, filepath.Join(d, name+".png"), composite(res))
}

var (
	sharedOnce sync.Once
	shared     Renderer
	sharedErr  error
)

func sharedRenderer(t testing.TB) Renderer {
	sharedOnce.Do(func() { shared, sharedErr = New(Config{Workers: 2}) })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return shared
}

func newClientT(t testing.TB) *client {
	r, err := New(Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r.(*client)
}

type stats struct {
	opaque, partial, yellow, magenta, dark, coloured int
}

func pixelStats(img *image.NRGBA) stats {
	var s stats
	if img == nil {
		return s
	}
	b := img.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := img.PixOffset(x, y)
			r, g, bl, a := int(img.Pix[i]), int(img.Pix[i+1]), int(img.Pix[i+2]), img.Pix[i+3]
			if a == 0 {
				continue
			}
			if a == 255 {
				s.opaque++
			} else {
				s.partial++
			}
			switch {
			case r > 200 && g > 180 && bl < 160:
				s.yellow++
			case r > 230 && g < 40 && bl > 230:
				s.magenta++
			case r+g+bl < 150:
				s.dark++
			}
			if max(r, g, bl)-min(r, g, bl) > 60 {
				s.coloured++
			}
		}
	}
	return s
}

func TestRenderFixtures(t *testing.T) {
	r := sharedRenderer(t)
	tests := []struct {
		file  string
		opts  Options
		check func(t *testing.T, res *Result)
	}{
		{"Test_all_tools.rm", Options{}, func(t *testing.T, res *Result) {
			wantPage(t, res, 1404, 1872)
			if s := pixelStats(res.Ink); s.dark < 10000 || s.coloured < 10000 {
				t.Errorf("ink stats %+v", s)
			}
			if res.Highlights == nil {
				t.Error("no highlights")
			}
		}},
		{"Landscape.rm", Options{Landscape: true}, func(t *testing.T, res *Result) {
			wantPage(t, res, 1872, 1404)
			if res.Ink.Rect.Dx() <= res.Ink.Rect.Dy() {
				t.Errorf("landscape image %v", res.Ink.Rect)
			}
		}},
		{"Page_down_test.rm", Options{}, func(t *testing.T, res *Result) {
			wantPage(t, res, 1404, 1872)
			if res.Extent.B < 4000 || res.Extent.T != 0 {
				t.Errorf("extent %+v, want growth below the page", res.Extent)
			}
			// ink below the page must actually be drawn
			below := res.Ink.SubImage(image.Rect(0, 2000, res.Ink.Rect.Dx(), res.Ink.Rect.Dy())).(*image.NRGBA)
			if s := pixelStats(below); s.opaque+s.partial == 0 {
				t.Error("no ink below the page")
			}
		}},
		{"Text_combos.rm", Options{}, func(t *testing.T, res *Result) {
			if s := pixelStats(res.Ink); s.dark < 5000 {
				t.Errorf("text page has little ink: %+v", s)
			}
		}},
		{"Wikipedia_highlighted_p1.rm", Options{}, func(t *testing.T, res *Result) {
			if res.Highlights == nil {
				t.Fatal("no highlights")
			}
			if s := pixelStats(res.Ink); s.yellow != 0 {
				t.Errorf("ink pass contains %d yellow pixels", s.yellow)
			}
			s := pixelStats(res.Highlights)
			if s.yellow < 10000 {
				t.Errorf("highlight pass has only %d yellow pixels", s.yellow)
			}
			if s.dark != 0 {
				t.Errorf("highlight pass contains %d dark (text?) pixels", s.dark)
			}
		}},
		{"Layers.rm", Options{}, func(t *testing.T, res *Result) {
			wantPage(t, res, 1404, 1872)
		}},
		{"RMPP_new_pens_and_colors.rm", Options{}, func(t *testing.T, res *Result) {
			// This file's SceneInfo declares paperSize [1404,1872].
			wantPage(t, res, 1404, 1872)
			if s := pixelStats(res.Ink); s.coloured < 10000 {
				t.Errorf("few coloured pixels: %+v", s)
			}
		}},
		{"test_rmpp_edges.rm", Options{}, func(t *testing.T, res *Result) {
			wantPage(t, res, 1620, 2160)
		}},
		{"Test_OOB.rm", Options{}, func(t *testing.T, res *Result) {
			wantPage(t, res, 1620, 2160)
			if res.Extent.R <= 1620 || res.Extent.B <= 2160 {
				t.Errorf("extent %+v, want growth right and down", res.Extent)
			}
		}},
		{"Test_PDF_500x1000.rm", Options{}, func(t *testing.T, res *Result) {
			if res.Extent.L >= 0 || res.Extent.R <= 1404 || res.Extent.B <= 1872 {
				t.Errorf("extent %+v", res.Extent)
			}
		}},
		{"Test_Images.rm", Options{}, func(t *testing.T, res *Result) {
			if s := pixelStats(res.Ink); s.magenta != 0 {
				t.Errorf("images disabled but %d magenta pixels", s.magenta)
			}
		}},
		{"Test_Images.rm", Options{Images: map[string]string{
			"4fd58983-6426-b49f-2349-43883eb6b61e":     filepath.Join(imageDir, "silly_cat.png"),
			"9324241f-7a5d-42ba-8c77-628a66612f76.png": filepath.Join(imageDir, "rainbow.png"), // by fileName
		}}, func(t *testing.T, res *Result) {
			s := pixelStats(res.Ink)
			if s.magenta > 100 {
				t.Errorf("%d magenta pixels: an image did not load", s.magenta)
			}
			if s.coloured < 20000 {
				t.Errorf("images missing: %+v", s)
			}
		}},
		{"Test_Images.rm", Options{Images: map[string]string{
			"4fd58983-6426-b49f-2349-43883eb6b61e": filepath.Join(imageDir, "silly_cat.png"),
		}}, func(t *testing.T, res *Result) {
			// one image missing: all images are disabled rather than drawing a magenta box
			if s := pixelStats(res.Ink); s.magenta != 0 || s.coloured > 1000 {
				t.Errorf("partial image map: %+v", s)
			}
		}},
		{"Test_all_tools.rm", Options{Scale: 0.5, Supersample: 3, Template: "P Grid medium"}, func(t *testing.T, res *Result) {
			if res.Ink.Rect.Dx() != 702 || res.Ink.Rect.Dy() != 936 {
				t.Errorf("size %v", res.Ink.Rect)
			}
		}},
	}
	for i, tt := range tests {
		name := fmt.Sprintf("%02d_%s", i, strings.TrimSuffix(tt.file, ".rm"))
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			res, err := r.RenderFile(context.Background(), fixture(tt.file), tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %v, page %dx%d, extent %+v, image %v, highlights %v",
				tt.file, time.Since(start).Round(time.Millisecond), res.PageW, res.PageH, res.Extent, res.Ink.Rect.Size(), res.Highlights != nil)
			checkGeometry(t, res, tt.opts)
			saveResult(t, name, res)
			tt.check(t, res)
		})
	}
}

func wantPage(t *testing.T, res *Result, w, h int) {
	t.Helper()
	if res.PageW != w || res.PageH != h {
		t.Errorf("page %dx%d, want %dx%d", res.PageW, res.PageH, w, h)
	}
}

func checkGeometry(t *testing.T, res *Result, opts Options) {
	t.Helper()
	scale := opts.Scale
	if scale == 0 {
		scale = 1
	}
	e := res.Extent
	if e.L > 0 || e.T > 0 || e.R < float64(res.PageW) || e.B < float64(res.PageH) {
		t.Errorf("extent %+v does not contain the page", e)
	}
	if w := int(math.Round((e.R - e.L) * scale)); res.Ink.Rect.Dx() != w {
		t.Errorf("ink width %d, want %d", res.Ink.Rect.Dx(), w)
	}
	if h := int(math.Round((e.B - e.T) * scale)); res.Ink.Rect.Dy() != h {
		t.Errorf("ink height %d, want %d", res.Ink.Rect.Dy(), h)
	}
	if res.Highlights != nil && res.Highlights.Rect != res.Ink.Rect {
		t.Errorf("highlight bounds %v != ink %v", res.Highlights.Rect, res.Ink.Rect)
	}
}

// TestGolden compares quarter-scale composites against testdata/golden.
// Regenerate with: go test -tags rmlines,... -run TestGolden -update
func TestGolden(t *testing.T) {
	r := sharedRenderer(t)
	cases := []struct {
		file string
		land bool
	}{
		{"Test_all_tools.rm", false},
		{"Landscape.rm", true},
		{"Page_down_test.rm", false},
		{"Text_combos.rm", false},
		{"Wikipedia_highlighted_p1.rm", false},
		{"Layers.rm", false},
		{"RMPP_new_pens_and_colors.rm", false},
		{"Test_PDF_500x1000.rm", false},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			res, err := r.RenderFile(context.Background(), fixture(c.file), Options{Landscape: c.land, Scale: goldenScale})
			if err != nil {
				t.Fatal(err)
			}
			got := composite(res)
			path := filepath.Join("testdata", "golden", strings.TrimSuffix(c.file, ".rm")+".png")
			if *update {
				savePNG(t, path, got)
				return
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			defer f.Close()
			want, err := png.Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			if want.Bounds() != got.Bounds() {
				t.Fatalf("bounds %v, golden %v", got.Bounds(), want.Bounds())
			}
			var sum float64
			bad := 0
			b := got.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c1 := got.NRGBAAt(x, y)
					r2, g2, b2, _ := want.At(x, y).RGBA()
					d := math.Abs(float64(c1.R)-float64(r2>>8)) + math.Abs(float64(c1.G)-float64(g2>>8)) + math.Abs(float64(c1.B)-float64(b2>>8))
					sum += d
					if d > 96 {
						bad++
					}
				}
			}
			n := float64(b.Dx() * b.Dy())
			if mean := sum / n / 3; mean > 1.5 || float64(bad)/n > 0.005 {
				savePNG(t, filepath.Join(outDir(t), "golden_mismatch_"+filepath.Base(path)), got)
				t.Errorf("differs from golden: mean channel diff %.2f, %d pixels off", mean, bad)
			}
		})
	}
}

// Layers.rm has hidden layers but they hold no strokes, so this checks the
// disabledLayers plumbing by hiding every layer of a page full of strokes.
func TestHiddenLayers(t *testing.T) {
	c := newClientT(t)
	shown, err := c.render(context.Background(), fixture("Test_all_tools.rm"), Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := c.render(context.Background(), fixture("Test_all_tools.rm"), Options{}, "hideall")
	if err != nil {
		t.Fatal(err)
	}
	saveResult(t, "Test_all_tools_all_layers_hidden", hidden)
	s, h := pixelStats(shown.Ink), pixelStats(hidden.Ink)
	t.Logf("visible ink %+v, all layers hidden %+v (highlights %v)", s, h, hidden.Highlights != nil)
	if h.opaque+h.partial > (s.opaque+s.partial)/100 || hidden.Highlights != nil {
		t.Error("hidden layers were drawn")
	}
}

// TestEdgeFringe compares edge pixels of our supersampled output with the
// library's own antialiasing, which blurs straight alpha and lightens edges.
func TestEdgeFringe(t *testing.T) {
	c := newClientT(t)
	for _, f := range []string{"Normal_A_stroke_2_layers_v3.2.2.rm", "Ballpoint_v2_all_colors.rm"} {
		ours, err := c.render(context.Background(), fixture(f), Options{}, "")
		if err != nil {
			t.Fatal(err)
		}
		lib, err := c.render(context.Background(), fixture(f), Options{}, "libaa")
		if err != nil {
			t.Fatal(err)
		}
		base := strings.TrimSuffix(f, ".rm")
		saveResult(t, base+"_supersampled", ours)
		saveResult(t, base+"_library_aa", lib)
		fo, fl := fringe(ours.Ink), fringe(lib.Ink)
		t.Logf("%s: mean edge lightening: supersampled %.1f, library aa %.1f", f, fo, fl)
		if fo > 8 || fo >= fl {
			t.Errorf("%s: supersampled edges lightened by %.1f (library %.1f)", f, fo, fl)
		}
	}
}

// fringe returns the mean amount by which partially transparent pixels are
// lighter than the darkest opaque pixel within 2 pixels.
func fringe(img *image.NRGBA) float64 {
	lum := func(i int) float64 {
		return 0.299*float64(img.Pix[i]) + 0.587*float64(img.Pix[i+1]) + 0.114*float64(img.Pix[i+2])
	}
	b := img.Rect
	var sum float64
	n := 0
	for y := b.Min.Y + 2; y < b.Max.Y-2; y++ {
		for x := b.Min.X + 2; x < b.Max.X-2; x++ {
			i := img.PixOffset(x, y)
			if a := img.Pix[i+3]; a == 0 || a == 255 {
				continue
			}
			ref := math.Inf(1)
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					j := img.PixOffset(x+dx, y+dy)
					if img.Pix[j+3] == 255 {
						ref = math.Min(ref, lum(j))
					}
				}
			}
			if math.IsInf(ref, 1) {
				continue
			}
			sum += math.Max(0, lum(i)-ref)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func TestCorruptFiles(t *testing.T) {
	c := newClientT(t)
	ctx := context.Background()
	if _, err := c.RenderFile(ctx, fixture("Layers.rm"), Options{}); err != nil {
		t.Fatal(err)
	}
	pid := c.workerPIDs()

	good, err := os.ReadFile(fixture("Test_all_tools.rm"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	garbage := append([]byte(nil), good[:43]...)
	for i := 0; i < 4096; i++ {
		garbage = append(garbage, byte(i*7919>>3))
	}
	cases := map[string]string{
		"truncated":   write("truncated.rm", good[:len(good)/2]),
		"header only": write("header.rm", good[:20]),
		"garbage":     write("garbage.rm", garbage),
		"empty":       write("empty.rm", nil),
		"v5":          "../../../../testdata/rm/v5/test_v5.rm",
		"missing":     filepath.Join(dir, "nope.rm"),
		"directory":   dir,
	}
	for name, path := range cases {
		res, err := c.RenderFile(ctx, path, Options{})
		switch {
		case err == nil:
			t.Logf("%s: rendered (%v); the library accepted it", name, res.Ink.Rect.Size())
		case errors.Is(err, ErrRender):
			t.Logf("%s: %v", name, err)
		default:
			t.Errorf("%s: %v, want ErrRender", name, err)
		}
	}
	if got := c.workerPIDs(); len(got) != 1 || got[0] != pid[0] {
		t.Errorf("worker restarted: %v -> %v", pid, got)
	}
	if _, err := c.RenderFile(ctx, fixture("Layers.rm"), Options{}); err != nil {
		t.Fatalf("render after corrupt files: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	c := newClientT(t)
	ctx := context.Background()
	_, err := c.render(ctx, fixture("Layers.rm"), Options{Timeout: 300 * time.Millisecond}, "sleep")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("sleep: %v, want ErrTimeout", err)
	}
	if len(c.workerPIDs()) != 0 {
		t.Error("timed-out worker still registered")
	}
	_, err = c.RenderFile(ctx, fixture("Test_all_tools.rm"), Options{Scale: 2, Timeout: time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("1ms: %v, want ErrTimeout", err)
	}
	if _, err := c.RenderFile(ctx, fixture("Layers.rm"), Options{}); err != nil {
		t.Fatalf("after timeout: %v", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := c.render(cctx, fixture("Layers.rm"), Options{}, "sleep"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context: %v", err)
	}
}

func TestWorkerCrash(t *testing.T) {
	c := newClientT(t)
	ctx := context.Background()
	if _, err := c.RenderFile(ctx, fixture("Layers.rm"), Options{}); err != nil {
		t.Fatal(err)
	}
	pids := c.workerPIDs()
	if len(pids) != 1 {
		t.Fatalf("pids %v", pids)
	}
	// Killed while idle: the next request respawns transparently.
	if err := syscall.Kill(pids[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RenderFile(ctx, fixture("Layers.rm"), Options{}); err != nil {
		t.Fatalf("after SIGKILL: %v", err)
	}
	if got := c.workerPIDs(); len(got) != 1 || got[0] == pids[0] {
		t.Errorf("pids %v after respawn (old %v)", got, pids)
	}

	// Crash inside a request (abort() in C): the request fails with
	// ErrWorkerCrashed, the next one succeeds.
	_, err := c.render(ctx, fixture("Layers.rm"), Options{}, "abort")
	if !errors.Is(err, ErrWorkerCrashed) {
		t.Fatalf("abort: %v, want ErrWorkerCrashed", err)
	}
	t.Logf("abort: %v", err)
	if _, err := c.RenderFile(ctx, fixture("Layers.rm"), Options{}); err != nil {
		t.Fatalf("after abort: %v", err)
	}
}

func TestPixelBudget(t *testing.T) {
	r, err := New(Config{MaxPixels: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = r.RenderFile(context.Background(), fixture("Layers.rm"), Options{})
	if !errors.Is(err, ErrRender) || !strings.Contains(err.Error(), "pixel budget") {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrent(t *testing.T) {
	r, err := New(Config{Workers: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	files := []string{"Text_combos.rm", "Test_all_tools.rm", "Wikipedia_highlighted_p1.rm", "Layers.rm"}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := r.RenderFile(context.Background(), fixture(files[i%len(files)]), Options{Scale: 0.5})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := len(r.(*client).workerPIDs()); n > 3 {
		t.Errorf("%d workers for a pool of 3", n)
	}
}

func TestIdleReap(t *testing.T) {
	r, err := New(Config{IdleTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c := r.(*client)
	if _, err := r.RenderFile(context.Background(), fixture("Layers.rm"), Options{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(c.workerPIDs()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("idle worker not reaped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := r.RenderFile(context.Background(), fixture("Layers.rm"), Options{}); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkRender measures one page per iteration through a warm worker.
func BenchmarkRender(b *testing.B) {
	for _, f := range []string{"Test_all_tools.rm", "Text_combos.rm", "Wikipedia_highlighted_p1.rm", "Page_down_test.rm"} {
		for _, ss := range []int{1, 2} {
			b.Run(fmt.Sprintf("%s/ss%d", strings.TrimSuffix(f, ".rm"), ss), func(b *testing.B) {
				r, err := New(Config{})
				if err != nil {
					b.Fatal(err)
				}
				defer r.Close()
				opts := Options{Supersample: ss}
				if _, err := r.RenderFile(context.Background(), fixture(f), opts); err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := r.RenderFile(context.Background(), fixture(f), opts); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
