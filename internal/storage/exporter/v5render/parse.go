package v5render

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/juruen/rmapi/encoding/rm"
)

// ErrCorrupt is returned by Parse for data that is not a well-formed v3/v5
// .rm page.
var ErrCorrupt = errors.New("v5render: corrupt .rm page")

// Sizes of the records in a v3/v5 .rm file, in bytes. All numbers are little
// endian; counts are uint32, everything else float32 or uint32.
const (
	layerMin  = 4                 // nbLines
	lineV3Min = 4 + 4 + 4 + 4 + 4 // brush type, colour, padding, brush size, nbPoints
	lineV5Min = lineV3Min + 4     // plus the v5 field after the brush size
	pointSize = 6 * 4             // x, y, speed, direction, width, pressure
)

// Parse decodes a v3 or v5 .rm page into the same structure as rmapi's
// (*rm.Rm).UnmarshalBinary, which allocates slices from the counts in the
// file before reading them: a corrupt count of 0xffffffff layers asks for
// about 100 GB and kills the process on Linux. Parse checks every count
// against the bytes that remain before allocating, so it never allocates more
// than a small multiple of len(data).
//
// Like rmapi it ignores trailing bytes after the last layer.
func Parse(data []byte) (*rm.Rm, error) {
	p := parser{data: data}
	if len(data) < rm.HeaderLen {
		return nil, fmt.Errorf("%w: short header", ErrCorrupt)
	}
	out := rm.New()
	switch string(data[:rm.HeaderLen]) {
	case rm.HeaderV5:
		out.Version = rm.V5
	case rm.HeaderV3:
		out.Version = rm.V3
	default:
		return nil, fmt.Errorf("%w: unknown header", ErrCorrupt)
	}
	p.off = rm.HeaderLen
	lineMin := lineV3Min
	if out.Version == rm.V5 {
		lineMin = lineV5Min
	}

	nbLayers, err := p.count("layers", layerMin)
	if err != nil {
		return nil, err
	}
	out.Layers = make([]rm.Layer, nbLayers)
	for i := range out.Layers {
		nbLines, err := p.count("lines", lineMin)
		if err != nil {
			return nil, err
		}
		lines := make([]rm.Line, nbLines)
		for j := range lines {
			l := &lines[j]
			l.BrushType = rm.BrushType(p.u32())
			l.BrushColor = rm.BrushColor(p.u32())
			l.Padding = p.u32()
			l.BrushSize = rm.BrushSize(p.f32())
			if out.Version == rm.V5 {
				l.Unknown = p.f32()
			}
			if p.short {
				return nil, fmt.Errorf("%w: truncated line", ErrCorrupt)
			}
			nbPoints, err := p.count("points", pointSize)
			if err != nil {
				return nil, err
			}
			if nbPoints == 0 {
				continue
			}
			l.Points = make([]rm.Point, nbPoints)
			for k := range l.Points {
				pt := &l.Points[k]
				pt.X, pt.Y, pt.Speed = p.f32(), p.f32(), p.f32()
				pt.Direction, pt.Width, pt.Pressure = p.f32(), p.f32(), p.f32()
			}
			// count guaranteed the points fit
		}
		out.Layers[i].Lines = lines
	}
	return out, nil
}

type parser struct {
	data  []byte
	off   int
	short bool // a read went past the end
}

func (p *parser) u32() uint32 {
	if len(p.data)-p.off < 4 {
		p.short = true
		p.off = len(p.data)
		return 0
	}
	v := binary.LittleEndian.Uint32(p.data[p.off:])
	p.off += 4
	return v
}

func (p *parser) f32() float32 { return math.Float32frombits(p.u32()) }

// count reads a record count and checks that that many records of at least
// minSize bytes each fit in the rest of the data.
func (p *parser) count(what string, minSize int) (int, error) {
	n := p.u32()
	if p.short {
		return 0, fmt.Errorf("%w: truncated %s count", ErrCorrupt, what)
	}
	rest := uint64(len(p.data) - p.off)
	if uint64(n)*uint64(minSize) > rest {
		return 0, fmt.Errorf("%w: %d %s do not fit in the remaining %d bytes", ErrCorrupt, n, what, rest)
	}
	return int(n), nil
}
