//go:build !cgo || !rmlines

package rmlines

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestStubNotAvailable(t *testing.T) {
	if Available() {
		t.Fatal("Available() = true in a stub build")
	}
	r, err := New(Config{})
	if !errors.Is(err, ErrNotAvailable) || r != nil {
		t.Fatalf("New = %v, %v; want nil, ErrNotAvailable", r, err)
	}
	var stderr bytes.Buffer
	if code := WorkerMain(strings.NewReader(""), &bytes.Buffer{}, &stderr); code == 0 {
		t.Fatal("stub WorkerMain returned 0")
	}
	if !strings.Contains(stderr.String(), "not compiled in") {
		t.Fatalf("stub WorkerMain stderr = %q", stderr.String())
	}
}
