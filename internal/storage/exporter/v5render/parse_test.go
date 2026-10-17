package v5render

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/juruen/rmapi/encoding/rm"
)

// Parse must produce exactly what rmapi produces for well-formed pages, and
// for prefixes of them (both fail or both give the same page). Every prefix
// of the first and last bytes is tried, and every 37th in between: rmapi's
// reflection-based reader makes all 90k prefixes of test_v3.rm take seconds.
func TestParseMatchesRmapi(t *testing.T) {
	for _, name := range []string{"test_v3.rm", "test_v5.rm"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatal(err)
			}
			want := rm.New()
			if err := want.UnmarshalBinary(data); err != nil {
				t.Fatal(err)
			}
			got, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("Parse differs from rmapi")
			}
			for n := 0; n < len(data); n++ {
				if n > 512 && n < len(data)-512 && n%37 != 0 {
					continue
				}
				w := rm.New()
				werr := w.UnmarshalBinary(data[:n])
				g, gerr := Parse(data[:n])
				if (werr == nil) != (gerr == nil) {
					t.Fatalf("prefix %d: rmapi err %v, Parse err %v", n, werr, gerr)
				}
				if gerr == nil && !reflect.DeepEqual(g, w) {
					t.Fatalf("prefix %d: Parse differs from rmapi", n)
				}
				if gerr != nil && !errors.Is(gerr, ErrCorrupt) {
					t.Fatalf("prefix %d: error %v is not ErrCorrupt", n, gerr)
				}
			}
		})
	}
}

func le(vs ...uint32) []byte {
	b := make([]byte, 0, 4*len(vs))
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, v)
	}
	return b
}

func page(header string, body ...uint32) []byte {
	return append([]byte(header), le(body...)...)
}

const huge = 0xffffffff

// Corrupt counts are rejected before anything is allocated for them.
func TestParseCorrupt(t *testing.T) {
	cases := map[string][]byte{
		"empty":          nil,
		"short header":   []byte("reMarkable .lines"),
		"bad header":     []byte("reMarkable .lines file, version=6          xxxx"),
		"no layer count": page(rm.HeaderV5),
		// the page from exporter.TestExportPageFailure
		"layers v5":               append([]byte(rm.HeaderV5), 0xff, 0xff, 0xff, 0xff, 0xff),
		"layers v3":               page(rm.HeaderV3, huge),
		"lines":                   page(rm.HeaderV5, 1, huge),
		"lines v3":                page(rm.HeaderV3, 1, 1<<28),
		"points":                  page(rm.HeaderV5, 1, 1, 17, 0, 0, 0, 0, huge),
		"points v3":               page(rm.HeaderV3, 1, 1, 17, 0, 0, 0, 1<<26),
		"short line":              page(rm.HeaderV5, 1, 1, 17, 0, 0, 0),
		"short points":            page(rm.HeaderV5, 1, 1, 17, 0, 0, 0, 0, 2, 1, 2, 3, 4, 5, 6, 7),
		"two layers, one missing": page(rm.HeaderV5, 2, 0),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, err := Parse(data)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
			d := after.TotalAlloc - before.TotalAlloc
			t.Logf("%v; allocated %d bytes", err, d)
			if d > 1<<20 {
				t.Errorf("allocated %d bytes for a %d-byte page", d, len(data))
			}
		})
	}
}

// The worst case: a count that just fits allocates a bounded multiple of the
// input (24-byte Layer per 4-byte count).
func TestParseAllocationBound(t *testing.T) {
	const n = 1 << 16
	data := page(rm.HeaderV5, n)
	data = append(data, make([]byte, 4*n)...) // n empty layers
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	p, err := Parse(data)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Layers) != n {
		t.Fatalf("%d layers", len(p.Layers))
	}
	d := after.TotalAlloc - before.TotalAlloc
	t.Logf("%d layers: allocated %d bytes for %d bytes of input", n, d, len(data))
	if d > uint64(16*len(data)) {
		t.Errorf("allocated %d bytes for a %d-byte page", d, len(data))
	}
}

func FuzzParse(f *testing.F) {
	for _, name := range []string{"test_v3.rm", "test_v5.rm"} {
		if data, err := os.ReadFile(filepath.Join(fixtureDir, name)); err == nil {
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err == nil && p == nil {
			t.Fatal("nil page without error")
		}
	})
}
