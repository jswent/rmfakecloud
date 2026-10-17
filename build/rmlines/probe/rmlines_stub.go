//go:build !cgo || !rmlines

package main

import (
	"errors"
	"image"
)

const available = false

func setDebug(bool) {}
func debug() bool   { return false }

func render(string) (*image.NRGBA, error) {
	return nil, errors.New("built without librm_lines (needs CGO_ENABLED=1 -tags rmlines)")
}
