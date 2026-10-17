package content

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func header(v string) []byte {
	h := "reMarkable .lines file, version=" + v
	return []byte(h + strings.Repeat(" ", HeaderLen-len(h)))
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want Format
	}{
		{"v3", header("3"), FormatV3},
		{"v5", header("5"), FormatV5},
		{"v6", header("6"), FormatV6},
		{"v6 with body", append(header("6"), 0, 0, 0, 1, 2, 3), FormatV6},
		{"v4", header("4"), FormatUnknown},
		{"two-digit version", header("60"), FormatUnknown},
		{"short v6 header without padding", []byte("reMarkable .lines file, version=6"), FormatUnknown},
		{"one byte short", header("6")[:HeaderLen-1], FormatUnknown},
		{"empty", nil, FormatUnknown},
		{"other file", bytes.Repeat([]byte{'%'}, 64), FormatUnknown},
		{"wrong case", []byte(strings.ToLower(string(header("6")))), FormatUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectFormat(tt.in); got != tt.want {
				t.Errorf("DetectFormat = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetectFormatFixtures checks the shared .rm samples in the repository.
func TestDetectFormatFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "testdata", "rm")
	tests := []struct {
		glob string
		want Format
	}{
		{filepath.Join(root, "v6", "*.rm"), FormatV6},
		{filepath.Join(root, "v5", "test_v5.rm"), FormatV5},
		{filepath.Join(root, "v5", "test_v3.rm"), FormatV3},
	}
	for _, tt := range tests {
		files, err := filepath.Glob(tt.glob)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no fixtures match %s", tt.glob)
		}
		for _, f := range files {
			t.Run(filepath.Base(f), func(t *testing.T) {
				fh, err := os.Open(f)
				if err != nil {
					t.Fatal(err)
				}
				defer fh.Close()
				got, err := DetectFormatReader(fh)
				if err != nil {
					t.Fatal(err)
				}
				if got != tt.want {
					t.Errorf("DetectFormatReader = %v, want %v", got, tt.want)
				}
			})
		}
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestDetectFormatReader(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		r       interface{ Read([]byte) (int, error) }
		want    Format
		wantErr error
	}{
		{"v5", bytes.NewReader(header("5")), FormatV5, nil},
		{"short", strings.NewReader("reMarkable"), FormatUnknown, nil},
		{"empty", strings.NewReader(""), FormatUnknown, nil},
		{"read error", errReader{boom}, FormatUnknown, boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectFormatReader(tt.r)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("format = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatString(t *testing.T) {
	for f, want := range map[Format]string{FormatUnknown: "unknown", FormatV3: "v3", FormatV5: "v5", FormatV6: "v6", Format(42): "unknown"} {
		if got := f.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(f), got, want)
		}
	}
}
