# `.content` fixtures and the schema they rely on

All files here are synthetic. Page and author ids are made up (each fixture
uses its own recognisable prefix, e.g. `e5000005-...-2f7a8b000003`); no
document, ink or metadata from anyone's device was copied. The field layout and
value shapes were modelled on the published schema and test files listed under
Sources.

| File | What it covers |
|---|---|
| `v1_notebook.content` + `v1_notebook.pagedata` | formatVersion 1 notebook: flat `pages`, empty `pageTags`, templates only in `.pagedata` (one name per line). |
| `v1_pdf_redirection.content` | formatVersion 1 PDF: `redirectionPageMap [0,1,-1,2,3,4]`, i.e. a page inserted by hand after PDF page 2; `originalPageCount` 5, `pageCount` 6. |
| `v2_notebook_paperpro.content` | formatVersion 2 notebook (Paper Pro style: 1620x2160 zoom fields, `Ballpointv2`/`ArgbCode` pen metadata). `cPages.pages` is **not** in `idx` order (inserted pages `bbb` and `bcab` were appended at the end of the array); `scrollTime`, `verticalScroll`, `modifed`, two `uuids`. |
| `v2_pdf_inserted_deleted.content` | formatVersion 2 landscape PDF of 4 pages: PDF page 2 deleted (`deleted.value 1`, `redir 1`), an inserted page `bab` (no `redir`) after page 1, an inserted page `bcb` that was deleted again. |
| `v2_pagetags.content` | formatVersion 2 notebook with object `pageTags` (two tags on one page, one tag on a deleted page), object `tags`, and non-integer `margins`, `textScale`, `lastOpenedPage`, `transform`. |

## What the parser implements

### Which page list

- formatVersion 1: `pages` (array of page ids, in order). Templates are not in
  `.content` but in `.pagedata`; `ApplyPagedata` reads it.
- formatVersion 2 (firmware 3.x): `cPages.pages[]`. The flat `pages` array is
  no longer written.
- Choice: a file that states `formatVersion` 1 is read from `pages`, any other
  from `cPages.pages`; if the preferred list is empty the other is used.
  *Assumption*: with no `formatVersion`, `cPages` wins. remarkable-mcp
  documents that a v1->v2 migrated document may still carry a stale
  `redirectionPageMap`, which argues for trusting `cPages` once it exists.
  giovi321's `NormalizePages` let the flat array win instead; no real file seen
  has both with different content.
- Neither list, `pageCount > 0`: `Parse` returns the `*Content` (no pages) and
  a `*NoPageListError` (`errors.Is(err, ErrNoPageList)`).

### Ordering rule for `cPages.pages[].idx.value`

`idx.value` is a fractional-index string. Pages are sorted by it with plain
byte-wise string comparison (Go `<`), stable for equal keys. That comparison
already puts a key immediately after its own prefix and before the next
sibling: `bb < bbb < bc < bcab < bcb < bd`. It is **not** "shorter keys
first": `bcab` must sort before `bcb`.

Evidence:
- Device-written `.content` files in RedTTGMoss/rm_api's tests
  (`rm_api/tests/files/content/last_page_visited.json`, `first_page.json`)
  list `ba, bb, bbb, bc, bcab, bcb, bd, bdb, bdbab, bdbb, bdc, be` in array
  order, which equals their byte-wise sort and disagrees with a
  length-then-lexicographic sort.
- rm_api's `Content.page_index_generator()` (rm_api/models.py), which Moss
  uses to write new documents the tablet accepts, emits `ba..bz, ca..nm, nna,
  nnba, ...`; I ran it for 800 keys and the output is strictly increasing under
  byte-wise comparison.
- rmapi-js documents `idx` as "values are like "aa", "ab", "ba", etc." without
  stating a rule.
- Nobody else sorts: rm_api/Moss, rmdocs, remarkable-mcp and
  zotero-remarkable-sync all use array order. Because the device appears to
  keep the array sorted, sorting is a no-op for every real file seen, and it
  protects against files where it is not (e.g. pages appended by a CRDT merge
  or by another tool).
- If any entry has no `idx`, array order is used for the whole list
  (*assumption*; no such device file seen).
- Equal keys (possible after concurrent inserts on two devices) keep array
  order. *Assumption*: the device may break ties by the `idx` timestamp's
  author; that is not known.

### `redir.value`

0-based index of the payload (PDF/EPUB) page shown under this page;
`Page.Redir = value + 1`. An entry with no `redir` is a page inserted on the
device (blank background); a negative value is treated the same. Sources:
rm_api `Page` ("If the document is not a notebook this will be on every page
except any user created pages"), rmdocs `parse.py` (`bg_pdf_page_idx =
page.get("redir").get("value")`), remarkable-mcp `_resolve_pdf_page_index`,
and rm_api's `pdf_redirect.json` (`ba -> 0, bb -> 1, ...`). Several pages may
point at the same payload page (duplicated page). *Assumption* for EPUB: redir
indexes the device's own pagination of the EPUB, which the server cannot
reproduce; `BuildPlan` passes it through and the orchestrator decides.

### `deleted.value`

Present with a non-zero number (or `true`) means the page was deleted on the
device; the entry is kept in the CRDT list but dropped from `Pages`. `0` or
`null` keeps the page. giovi321 tested `value > 0`; zotero-remarkable-sync
keeps `value === 0`; rm_api drops any entry with a truthy `deleted` object,
which would also drop `value 0` (we do not follow that).

### Other per-page fields

- `template.value`: template name as stored ("Blank", "P Grid medium",
  "LS Lines medium"). rm_api defaults a missing template to "Blank"; we leave
  it empty and let the renderer default.
- `scrollTime` (`{"timestamp","value": ISO 8601 date}`) and `verticalScroll`
  (number, device pixels scrolled down a grown page): view state only, not
  needed for export, ignored. Shapes from rmapi-js `src/raw.ts`.
- `modifed` (sic, bare string, epoch ms): ignored.
- Every value is wrapped as `{"timestamp": "A:C", "value": ...}` (an LWW
  register; `A` appears to index `cPages.uuids[].second`, the author). The
  timestamp is a string and is never parsed.

### `cPages` top level

`lastOpened.value` (page id) -> `LastOpenedPageID`; `original.value` (payload
page count at import, `-1` for notebooks) -> `OriginalPageCount` when the top
level `originalPageCount` is absent; `uuids` ignored.

### formatVersion 1 `redirectionPageMap`

`redirectionPageMap[i]` is the 0-based payload page under `pages[i]`; `-1`
means an inserted page. Sources: bordaigorl/remy `pdfbase.py`
(`originalPageNum`: `i = pmap[i]; if i < 0: i = None`), rmdocs `parse.py`,
remarkable-mcp. *Assumption*: an index past the end of the map, or a
non-numeric entry, is an inserted page.

### `pageTags`

formatVersion 2: objects `{"name", "pageId", "timestamp"}` (timestamp: epoch ms
number), per rmapi-js `PageTag` and giovi321 71615fd. Older files: strings;
those cannot be tied to a page and are kept only in `Content.PageTags`. Each
element is decoded on its own, so a mixed list does not fail.

### Leniency

Only "not a JSON object" fails `Parse`. Every other field is decoded on its
own; wrong types are logged at debug level and skipped. Numbers may come as
strings (`sizeInBytes`, sometimes counts) or with fractions (`margins`,
`textScale`, `lastOpenedPage`, `customZoom*`).

### Page plan assumption

`BuildPlan`: for a `pdf` payload whose `.content` holds no redirection at all
(no `redirectionPageMap`, no `redir` on any entry), page i is paired with PDF
page i+1 when the page count matches the payload (`originalPageCount`, else
`pageCount`, unknown counts as a match). This mirrors what rmapi and the old
exporter did for formatVersion 1 files. An explicit map that says every page
is inserted (`[-1, -1]`) is respected and does not trigger the fallback.

## `.rm` headers

43 bytes: `reMarkable .lines file, version=N` padded with spaces (rmapi
`encoding/rm` `HeaderV3`/`HeaderV5`/`HeaderLen`; rmscene `HEADER_V6`). Checked
against the shared samples in `testdata/rm/`.

## Sources

- erikbrinkman/rmapi-js, `src/raw.ts` (zod schema for `.content`, `CPagePage`,
  `PageTag`): https://github.com/erikbrinkman/rmapi-js/blob/main/src/raw.ts
- RedTTGMoss/rm_api (Moss desktop's API), `rm_api/models.py` (`Page`,
  `CPages`, `page_index_generator`) and `rm_api/tests/files/content/*.json`:
  https://github.com/RedTTGMoss/rm_api
- Seb-sti1/rmdocs, `rmdocs/parse.py` (formatVersion 1 vs 2 page lists):
  https://github.com/Seb-sti1/rmdocs/blob/main/rmdocs/parse.py
- bordaigorl/remy, `remy/remarkable/pdfbase.py` (redirectionPageMap, -1):
  https://github.com/bordaigorl/remy/blob/master/remy/remarkable/pdfbase.py
- lucasrla/remarks, `remarks/utils.py` (`get_pages_data`):
  https://github.com/lucasrla/remarks/blob/main/remarks/utils.py
- SamMorrowDrums/remarkable-mcp, `remarkable_mcp/extract.py`
  (`_resolve_pdf_page_index`, cPages authoritative over stale maps):
  https://github.com/SamMorrowDrums/remarkable-mcp
- bpiwowar/zotero-remarkable-sync, `src/modules/remarkable/rmdoc.ts`
  (deleted filter): https://github.com/bpiwowar/zotero-remarkable-sync
- giovi321 commits 2323622 (cPages, deleted) and 71615fd (object pageTags) in
  the joagonca#4 branch, as summarised in the project's triage report.
- juruen/rmapi `archive/file.go` (formatVersion 1 `Content`, which this
  package replaces on the export path).

ricklupton/rmscene and rmc parse `.rm` only, not `.content`; rschroll/rmrl
predates formatVersion 2. Neither has anything on `cPages`.
