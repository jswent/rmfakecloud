package templates

// Kind selects the generator of a template.
type Kind int

const (
	// Blank draws nothing.
	Blank Kind = iota
	// Lines draws horizontal rules, with an optional heading rule and an
	// optional vertical margin rule.
	Lines
	// Grid draws horizontal and vertical lines.
	Grid
	// Dots draws a small square at every grid crossing.
	Dots
)

// Pos is an x position in page units: from the left edge of the page, or,
// with Canvas, from the left edge of the square canvas the device lays its
// portrait templates out on (see page.originX).
type Pos struct {
	X      float64
	Canvas bool
}

func onCanvas(x float64) Pos { return Pos{X: x, Canvas: true} }
func onPage(x float64) Pos   { return Pos{X: x} }

// Spec is the geometry of one template in page units (the tablet's page
// coordinates: origin top-left, y down; the same scale on the reMarkable 2,
// Paper Pro and Paper Pro Move). Rules are drawn RuleWidth wide in RuleGrey,
// the style measured on the tablet's export.
type Spec struct {
	Kind Kind
	// Spacing between rules, grid lines or dots.
	Spacing float64
	// FirstY is the y of the first horizontal rule; rules repeat every
	// Spacing to the bottom of the page, grown part included. FirstYMobile,
	// when not 0, replaces it on the Move. Grids and dots start at y 0.
	FirstY, FirstYMobile float64
	// Canvas: the rules of a Lines template run across the square canvas
	// (the whole width of a portrait page) instead of the page width.
	Canvas bool
	// Heading is the y of the heading rule, 0 for none. It runs from
	// HeadingX to the page width minus HeadingX (HeadingXMobile on the
	// Move).
	Heading, HeadingX, HeadingXMobile float64
	// Margin is a vertical rule over the whole page height (Lines), if
	// HasMargin.
	Margin    Pos
	HasMargin bool
	// Columns is the x of one vertical grid line or dot column, for a
	// portrait page, a landscape page, a Move portrait page and a Move
	// landscape page; columns repeat every Spacing.
	Columns [4]Pos
	// RightOnly: the grid starts at its column and extends to the right
	// only, horizontal lines included (the "Grid margin" templates).
	RightOnly bool
	// Measured is true when the geometry was read off the tablet's own PDF
	// export; otherwise it comes from the device's template files and how
	// the tablet draws them is assumed from the measured template.
	Measured bool
}

// Rule style of the tablet's PDF export, measured on "P Lined heading":
// 0.5 units wide, grey 192/255 (the template files give no stroke width or
// colour; this is the device's default).
const (
	RuleWidth = 0.5
	RuleGrey  = 0.752941
)

// The measured "P Lined heading" layout.
const (
	linedSpacing  = 78.7
	linedFirstY   = 287
	headingY      = 187
	headingX      = 218
	headingXMove  = 700 // the rule runs from 700 to W-700 = 120.3 on the Move
	mobileFirstY  = 160 // Lines templates on the Move
	pMarginX      = 478 // P Margin: canvas x, 244 on rM2
	lsMarginX     = 243 // LS Margin: page x
	gridMedium    = 78.3
	gridSmall     = 52
	gridLarge     = 104
	dotsSmall     = 42.5
	dotsLarge     = 78
	linesSmall    = 52
	linesLarge    = 97
	linesMediumY  = 177.8
	linesLargeY   = 162
	marginSmallY  = 154
	gridSmallOff  = -25
	gridLargeOff  = -78
	gridMarginMed = 469
	gridMarginLg  = 497
)

func lines(spacing, firstY float64) Spec {
	return Spec{Kind: Lines, Spacing: spacing, FirstY: firstY, FirstYMobile: mobileFirstY, Canvas: true}
}

func lsLines(spacing, firstY float64) Spec {
	return Spec{Kind: Lines, Spacing: spacing, FirstY: firstY, FirstYMobile: mobileFirstY}
}

func withMargin(s Spec, x Pos) Spec {
	s.Margin, s.HasMargin = x, true
	return s
}

func grid(size float64, col Pos) Spec {
	return Spec{Kind: Grid, Spacing: size, Columns: [4]Pos{col, col, col, col}}
}

func gridMargin(size float64, col Pos) Spec {
	s := grid(size, col)
	s.RightOnly = true
	return s
}

var registry = map[string]Spec{
	"Blank":    {Kind: Blank, Measured: true},
	"P Blank":  {Kind: Blank, Measured: true},
	"LS Blank": {Kind: Blank, Measured: true},

	"P Lined heading": {Kind: Lines, Spacing: linedSpacing, FirstY: linedFirstY, Canvas: true,
		Heading: headingY, HeadingX: headingX, HeadingXMobile: headingXMove, Measured: true},

	"P Lines small":  lines(linesSmall, 0),
	"P Lines medium": lines(linedSpacing, linesMediumY),
	"P Lines large":  lines(linesLarge, linesLargeY),

	"P Margin small":  withMargin(lines(linesSmall, marginSmallY), onCanvas(pMarginX)),
	"P Margin medium": withMargin(lines(linedSpacing, linesMediumY), onCanvas(pMarginX)),
	"P Margin large":  withMargin(lines(linesLarge, linesLargeY), onCanvas(pMarginX)),

	"LS Lines small":   lsLines(linesSmall, 0),
	"LS Lines medium":  lsLines(linedSpacing, linesMediumY),
	"LS Margin small":  withMargin(lsLines(linesSmall, marginSmallY), onPage(lsMarginX)),
	"LS Margin medium": withMargin(lsLines(linedSpacing, linesMediumY), onPage(lsMarginX)),

	// Grids: horizontal lines from y 0, vertical lines through the column;
	// the device offers the same files on landscape pages.
	"P Grid small":  grid(gridSmall, onCanvas(gridSmallOff)),
	"P Grid medium": grid(gridMedium, onCanvas(0)),
	"P Grid large":  grid(gridLarge, onCanvas(gridLargeOff)),

	"P Grid margin med":    gridMargin(gridMedium, onCanvas(gridMarginMed)),
	"P Grid margin large":  gridMargin(gridLarge, onCanvas(gridMarginLg)),
	"LS Grid margin med":   gridMargin(gridMedium, onPage(234)),
	"LS Grid margin large": gridMargin(gridLarge, onPage(312)),

	// Dots: a 1x1 unit square at the top-left of each tile, filled black
	// and stroked 1 unit wide. Columns per the files' offsetX expressions.
	"P Dots S": {Kind: Dots, Spacing: dotsSmall, Columns: [4]Pos{
		onCanvas(-22 + 1), onCanvas(-22 + 1), onPage(5.5 + 1), onPage(5.5 + 1)}},
	"P Dots large": {Kind: Dots, Spacing: dotsLarge, Columns: [4]Pos{
		onCanvas(-1 - 2), onPage(0 - 2), onPage(19.5 - 2), onPage(26 - 2)}},
}

// Lookup returns the geometry of the template the device stores as name
// (the "filename" of its templates.json, as written in the .content file).
func Lookup(name string) (Spec, bool) {
	s, ok := registry[name]
	return s, ok
}

// Names returns the known template names.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}
