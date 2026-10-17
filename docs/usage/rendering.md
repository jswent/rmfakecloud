# Document rendering

The web interface downloads documents as PDF, with your handwriting and
highlights drawn on them. This page describes what is drawn and what is not.

## What is rendered

| Content | How |
| ------- | --- |
| Pages written with tablet software 3.x (`.rm` version 6) | Rendered by [LIB rMLines](https://github.com/RedTTGMoss/librm_lines) into images, in separate worker processes |
| Pages written with software 1.x / 2.x (`.rm` versions 3 and 5) | Drawn as vector strokes |
| Typed text (software 3.x) | Rendered with the pages, in the library's built-in fonts (Karla, Libre Baskerville), not the tablet's |
| Highlighter and text highlights | Blended over the page (multiply), so text underneath stays readable |
| Annotated PDFs | Ink is drawn onto the original PDF pages; pages added on the tablet are inserted, pages deleted on the tablet are left out, and pages are in the order shown on the tablet |
| Notebooks | One page per notebook page, at the size of the tablet's own PDF export: about 445 x 594 pt (reMarkable 1 and 2), 514 x 685 pt (Paper Pro), 260 x 463 pt (Paper Pro Move). Pages that were extended downwards on the tablet are exported at their full length |

Ink written outside the page width, or above the page, is cut off, as in the
tablet's own export (set `RMLINES_CLIP=false` to keep it).

## Limits

- **Templates are not drawn.** Lined, grid and dotted page templates are not
  reproduced; pages have a white background.
- **Images are not drawn.** Images placed on pages are left out.
- **EPUB documents have no background.** The tablet lays out EPUB pages
  itself, and that layout cannot be reproduced, so the ink of an EPUB is
  exported on blank pages.
- **Password-protected PDFs** are exported with the ink only, without the
  original pages.
- **Pen rendering differs from the tablet.** Stroke widths and textures are
  close to, but not the same as, the tablet's export.
- A page that cannot be rendered (corrupt file, over the time limit) is
  exported as a blank page and logged; the rest of the document is still
  exported.

## Builds without the renderer

The renderer for software 3.x pages is a C++ library linked into the server
with cgo. The container images for amd64 and arm64 include it. Builds without
it (a plain `go build`, `make build`, the armv7 image, Windows binaries)
export software 3.x pages as blank pages and say so in the log at startup.
To build it yourself, see `internal/storage/exporter/rmlines/README.md` and
`make build-rmlines`.

## Settings and cache

The renderer's settings (`RMLINES_*`) are listed in
[Configuration](../install/configuration.md#document-rendering). Exports are
cached per document version in the user's `.cache` folder inside the data
directory and rebuilt when the document changes.
