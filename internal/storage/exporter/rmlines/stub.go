//go:build !cgo || !rmlines

package rmlines

import (
	"fmt"
	"io"
)

// Available reports whether the v6 renderer is compiled in.
func Available() bool { return false }

// New returns ErrNotAvailable in builds without cgo and the rmlines tag.
func New(cfg Config) (Renderer, error) { return nil, ErrNotAvailable }

// WorkerMain reports that the renderer is not compiled in and returns 2.
func WorkerMain(stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	fmt.Fprintln(stderr, ErrNotAvailable)
	return 2
}
