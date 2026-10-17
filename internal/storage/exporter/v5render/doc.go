// Package v5render draws the ink of a reMarkable .rm page in format
// version 3 or 5 (tablet software 1.x and 2.x) as a PDF content stream.
//
// The stream is written in visual PDF user space: points, origin at the
// bottom-left of the visible page box, y up, with the page size given in
// Options. The caller (inkpdf.StampContent) maps that space onto the real
// page, so rotation and box offsets are not handled here.
//
// # Geometry
//
// Ink is stored in device pixels of the portrait 1404x1872 screen, origin
// top-left, y down. The mapping is the one of the unipdf renderer this
// package replaced (internal/storage/exporter/pdf.go, since removed; see the
// git history: pageTransform.apply and transformFor, including giovi321's
// quarter turn from ddvk/rmfakecloud#483, commit 430ac58):
//
//   - Turned (landscape): the page's width runs along the screen's long side.
//     s = PageW/1872, x = (1872 - y_dev)*s, y = PageH - x_dev*s.
//   - Upright (portrait): s = PageH/1872, or PageW/1404 when PageH/PageW < 1.33;
//     x = x_dev*s, y = PageH - y_dev*s.
//
// That renderer turned the ink when the page is wider than tall (rmrl does the same:
// rmrl/render.py, landscape_bpage). Render also turns it when
// Options.Landscape is set: a portrait page shown in a landscape document is
// fitted to the screen's long side, which is the same turned mapping.
//
// # Pens
//
// Every point carries the width the tablet computed for it (Point.Width, in
// device pixels, about 4 for a medium fineliner). Widths below are that value
// times s. The model is deliberately simple and deterministic; it is in the
// spirit of rmrl (github.com/rschroll/rmrl, rmrl/pens/*.py) and rM2svg
// (github.com/lschwetlick/maxio, rm_tools/rM2svg.py), which derive width
// from Width, pressure, tilt and speed with hand-tuned formulas, without
// copying their constants:
//
//   - Fineliner, Ballpoint, Marker: one width per stroke, the mean of Point.Width.
//   - Mechanical (sharp) pencil: constant width, colour lightened to 70%
//     strength (rM2svg uses opacity 0.7 for this pen).
//   - Paintbrush and Calligraphy: per segment, width = W*(0.5 + 0.5*p), where W
//     and p are the mean Width and Pressure of the segment's two points
//     (pressure clamped to [0,1]). rmrl's paintbrush uses
//     W*(0.75 + 0.5625*(p-1)); ours keeps half the width at zero pressure so
//     light strokes stay visible.
//   - Tilt pencil: per segment, width = W, colour strength 0.4 + 0.6*p, so
//     pressure shows as darkness (rM2svg and rmrl's pencil map pressure to
//     opacity). Strength lightens the colour towards white instead of using
//     transparency, so no extra graphics states are needed.
//   - Highlighter: the whole stroke as one polyline, width = mean Width (30 px
//     when the file has none, the width the old renderer used), square caps, drawn
//     under an ExtGState with CA 0.5 and BM Multiply so text underneath
//     stays legible. Colour 0..2 (older files) is yellow.
//   - Eraser and erase-area strokes are skipped, as in the old renderer.
//   - Unknown pen codes are drawn like a fineliner.
//
// Consecutive segments whose width and colour round to the same value are
// joined into one path. Lines use round caps and joins.
//
// Pen codes: rmapi's constants (v3: 0..8, v5: 12..18) plus 21 for
// calligraphy, which rmapi lacks (rmrl/pens/__init__.py PEN_MAPPING).
//
// Colours: rmapi has only black, grey and white. Later 2.x software writes
// further codes, parsed numerically here: 3 yellow, 4 green, 5 pink, 6 blue,
// 7 red, 8 grey overlap (rmscene's PenColor enum). RGB values follow rmc's
// RM_PALETTE (github.com/ricklupton/rmc,
// src/rmc/exporters/writing_tools.py). The old renderer set the
// colour with rg, the fill operator, so strokes always came out black, and
// it swapped black and white; Render uses RG and the right values.
package v5render
