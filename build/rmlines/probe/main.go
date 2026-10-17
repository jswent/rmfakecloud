// Command probe is a link smoke test for the static librm_lines build.
//
// It calls setDebugMode/getDebugMode and, given a .rm file and an output
// path, renders the page to PNG and fails unless some ink was drawn.
//
//	CGO_ENABLED=1 CGO_LDFLAGS=-L<dir>/lib go run -tags rmlines,rmlines_static . in.rm out.png
//
// Without the tags it builds a stub that reports the library as unavailable.
package main

import (
	"fmt"
	"image/png"
	"os"
	"runtime"
)

func init() { runtime.LockOSThread() }

func main() {
	fmt.Println("rmlines available:", available)
	if !available {
		os.Exit(2)
	}
	setDebug(true)
	on := debug()
	setDebug(false)
	off := debug()
	fmt.Printf("getDebugMode: after true=%v, after false=%v\n", on, off)
	if !on || off {
		fail("setDebugMode/getDebugMode round trip failed")
	}
	if len(os.Args) != 3 {
		return
	}
	img, err := render(os.Args[1])
	if err != nil {
		fail(err.Error())
	}
	ink := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			ink++
		}
	}
	f, err := os.Create(os.Args[2])
	if err != nil {
		fail(err.Error())
	}
	if err := png.Encode(f, img); err != nil {
		fail(err.Error())
	}
	if err := f.Close(); err != nil {
		fail(err.Error())
	}
	b := img.Bounds()
	fmt.Printf("rendered %dx%d, %d inked pixels -> %s\n", b.Dx(), b.Dy(), ink, os.Args[2])
	if ink == 0 {
		fail("render produced no ink")
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "probe:", msg)
	os.Exit(1)
}
