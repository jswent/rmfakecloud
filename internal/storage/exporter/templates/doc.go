// Package templates draws reMarkable page templates (lined, grid and dot
// rules) as PDF content streams, to go under the ink of exported notebook
// pages as the tablet's own PDF export does.
//
// Render takes the template name as the device stores it in the .content
// file ("P Lined heading", "P Grid medium", "LS Lines small", ...) and the
// visual page size in points, and returns a stream in visual PDF user space
// (bottom-left origin, y up) for inkpdf.StampContent. Geometry is in page
// units (the tablet's page coordinates, origin top-left, y down) at 72/227
// pt per unit, the matrix the tablet's export writes (0.317180616 0 0
// -0.317180616 0 H cm), so the same table serves the reMarkable 2 (1404 x
// 1872 units), Paper Pro (1620 x 2160) and Paper Pro Move (820 x 1458).
//
// # Measured: "P Lined heading"
//
// Read off the content streams of the tablet's own exports (tablet software
// 3.x, PDFium): each rule is a separate stroked segment in page units.
//
//	page                              size pt    rules  heading rule       rules: y (units)       x extent (units)
//	AI training p3 (Paper Pro)        514x685     1+24  y 187, x 218..1402  287 + 78.7k, k=0..23   -270..1890
//	Sites p3 (reMarkable 2)           445x594     1+21  y 187, x 218..1186  287 + 78.7k, k=0..20   -234..1638
//	Move p2 (Paper Pro Move)          260x463     1+15  y 187, x 120.3..700 287 + 78.7k, k=0..14   -319..1139.3
//	AI training p1 (Paper Pro, grown) 514x2007    1+77  y 187, x 218..1402  287 + 78.7k, k=0..76   -270..1890
//
// Every rule, heading included: 0.5 units wide (0.159 pt), grey 0.752941
// (192/255) in a gray-equivalent RGB colour space, square caps. The spacing
// is 78.7 units on all three devices. The heading rule has the same weight
// as the others; only its position differs. Rules are as long as the
// device page is tall and centred on the page (x from W/2 - H/2), so they
// cover the page width; on a grown page they keep that length and continue
// every 78.7 units down to the bottom of the page (every rule with
// y < page height; 25 pages checked, the last rule lies 9 to 73 units above
// the bottom edge), and the heading rule is drawn only once, at the top.
// On the Move the heading runs from x 120.3 to x 700 (its canvas is 820.3
// units wide), i.e. inset W - 700 instead of 218.
//
// # The other templates
//
// The tablet's template files (software 3.x, /usr/share/remarkable/
// templates/*.template, JSON with constants and expressions over
// templateWidth and templateHeight) give the geometry of the others. Those
// numbers are exact, but how the tablet draws them is assumed to match the
// measured template (same stroke width and grey: the files give neither).
// They were read from a copy of the files published in
// github.com/cheeragpatel/obsidian-sync (rm-templates/templates); no file
// is copied here. The same files define "P Lined heading" as measured
// above (magicHeadingOffsetX 218, 700 below a width of 1000; magicHeadingOffsetY
// 187; magicOffsetY 287; row height 78.7), which supports reading the
// others the same way. "Move" below means a page narrower than 1000 units
// (the files' mobileMaxWidth); canvas x is relative to W/2 - H/2.
//
//	name                     status       geometry (units)
//	P Lined heading          MEASURED     see above
//	P Lines small            APPROXIMATE  rules y 0 + 52k (Move: 160 + 52k)
//	P Lines medium           APPROXIMATE  rules y 177.8 + 78.7k (Move: 160 + ...)
//	P Lines large            APPROXIMATE  rules y 162 + 97k (Move: 160 + ...)
//	P Margin small           APPROXIMATE  rules y 154 + 52k; margin at canvas x 478 (244 on rM2)
//	P Margin medium / large  APPROXIMATE  as P Lines medium / large, margin at canvas x 478
//	LS Lines small / medium  APPROXIMATE  as P, across the landscape page width
//	LS Margin small / medium APPROXIMATE  as P Margin, margin at page x 243
//	P Grid small             APPROXIMATE  52; lines y 52k, x canvas -25 + 52k
//	P Grid medium            APPROXIMATE  78.3; lines y 78.3k, x canvas 78.3k
//	P Grid large             APPROXIMATE  104; lines y 104k, x canvas -78 + 104k
//	P Grid margin med/large  APPROXIMATE  78.3 / 104 grid right of canvas x 469 / 497
//	LS Grid margin med/large APPROXIMATE  78.3 / 104 grid right of page x 234 / 312
//	P Dots S                 APPROXIMATE  42.5; 1x1 squares at canvas x -21 + 42.5k, y 42.5k (Move: x 6.5)
//	P Dots large             APPROXIMATE  78; 1x1 squares at canvas x -3 + 78k (landscape -2, Move 17.5 / 24), y 78k
//	Blank, P Blank, LS Blank MEASURED     nothing
//
// Dots are filled black and stroked 1 unit wide, as the files ask
// (fillColor #000000, strokeWidth 1); the stroke colour is the assumed
// default grey. The P Grid and P Dots files are also offered for landscape
// pages and are drawn the same way on them. Other names (planners,
// storyboards, music, checklists, hexagons, perspective, "Lined top/bottom",
// ...) are not drawn: the page stays blank and one debug line is logged per
// name.
package templates
