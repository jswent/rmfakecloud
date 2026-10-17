//go:build cgo && rmlines

package main

/*
#cgo LDFLAGS: -lrm_lines
#cgo rmlines_static,darwin LDFLAGS: -lharfbuzz -lfreetype -lsimdutf -lc++ -framework CoreText -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices
#cgo rmlines_static,linux LDFLAGS: -lharfbuzz -lfreetype -lsimdutf -l:libstdc++.a -lm -static-libgcc
#include <stdlib.h>
#include "rm_lines_c.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"image"
	"unsafe"
)

const available = true

func setDebug(b bool) { C.setDebugMode(C.bool(b)) }
func debug() bool     { return bool(C.getDebugMode()) }

// goStr copies a library-owned (thread_local) string. main locks the OS
// thread, so the copy happens on the thread that produced it.
func goStr(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}

func render(path string) (*image.NRGBA, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	treeID := goStr(C.buildTree(cp))
	if treeID == "" {
		return nil, errors.New("buildTree failed")
	}
	ct := C.CString(treeID)
	defer C.free(unsafe.Pointer(ct))
	defer C.destroyTree(ct)

	w, h := 1404, 1872
	var info struct {
		PaperSize []int `json:"paperSize"`
	}
	if si := goStr(C.getSceneInfo(ct)); si != "" &&
		json.Unmarshal([]byte(si), &info) == nil && len(info.PaperSize) == 2 &&
		info.PaperSize[0] > 0 && info.PaperSize[1] > 0 {
		w, h = info.PaperSize[0], info.PaperSize[1]
	}

	rendererID := goStr(C.makeRenderer(ct, 0, false))
	if rendererID == "" {
		return nil, errors.New("makeRenderer failed")
	}
	cr := C.CString(rendererID)
	defer C.free(unsafe.Pointer(cr))
	defer C.destroyRenderer(cr)

	n := C.size_t(w * h * 4)
	buf := C.malloc(n)
	if buf == nil {
		return nil, errors.New("out of memory")
	}
	defer C.free(buf)
	C.getFrame(cr, (*C.uint32_t)(buf), n, 0, 0, C.int(w), C.int(h), C.int(w), C.int(h), true)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, unsafe.Slice((*byte)(buf), int(n)))
	return img, nil
}
