package config

import (
	"math"
	"testing"
	"time"
)

func TestRenderFromEnv(t *testing.T) {
	if r := renderFromEnv(); r != (RenderConfig{}) {
		t.Fatalf("defaults %+v", r)
	}
	t.Setenv(envRmlinesDisable, "true")
	t.Setenv(envRmlinesWorkers, "3")
	t.Setenv(envRmlinesScale, "1.5")
	t.Setenv(envRmlinesSupersample, "1")
	t.Setenv(envRmlinesTimeout, "45")
	t.Setenv(envRmlinesDocTimeout, "15m")
	t.Setenv(envRmlinesPtPerPx, "72/226")
	t.Setenv(envRmlinesClip, "false")
	t.Setenv(envRmlinesTemplates, "false")
	r := renderFromEnv()
	want := RenderConfig{Disable: true, Workers: 3, Scale: 1.5, Supersample: 1, Timeout: 45 * time.Second, DocTimeout: 15 * time.Minute, PtPerPx: 72.0 / 226, NoClip: true, NoTemplates: true}
	if r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}

	t.Setenv(envRmlinesTimeout, "2m")
	t.Setenv(envRmlinesPtPerPx, "0.3172")
	if r := renderFromEnv(); r.Timeout != 2*time.Minute || math.Abs(r.PtPerPx-0.3172) > 1e-12 {
		t.Fatalf("got %+v", r)
	}

	for _, bad := range [][2]string{
		{envRmlinesWorkers, "0"}, {envRmlinesScale, "x"}, {envRmlinesSupersample, "9"},
		{envRmlinesTimeout, "-1s"}, {envRmlinesDocTimeout, "soon"}, {envRmlinesPtPerPx, "72/0"}, {envRmlinesDisable, "maybe"},
	} {
		t.Run(bad[0], func(t *testing.T) {
			t.Setenv(bad[0], bad[1])
			r := renderFromEnv()
			switch bad[0] {
			case envRmlinesWorkers:
				if r.Workers != 0 {
					t.Errorf("workers %d", r.Workers)
				}
			case envRmlinesScale:
				if r.Scale != 0 {
					t.Errorf("scale %v", r.Scale)
				}
			case envRmlinesSupersample:
				if r.Supersample != 0 {
					t.Errorf("supersample %d", r.Supersample)
				}
			case envRmlinesTimeout:
				if r.Timeout != 0 {
					t.Errorf("timeout %v", r.Timeout)
				}
			case envRmlinesDocTimeout:
				if r.DocTimeout != 0 {
					t.Errorf("doc timeout %v", r.DocTimeout)
				}
			case envRmlinesPtPerPx:
				if r.PtPerPx != 0 {
					t.Errorf("pt per px %v", r.PtPerPx)
				}
			case envRmlinesDisable:
				if r.Disable {
					t.Error("disable from an invalid value")
				}
			}
		})
	}
}
