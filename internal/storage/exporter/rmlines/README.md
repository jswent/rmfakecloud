# rmlines: v6 `.rm` rendering with LIB rMLines

This package renders reMarkable v6 `.rm` pages (firmware 3.x) to bitmaps with
[LIB rMLines](https://github.com/RedTTGMoss/librm_lines) (Apache-2.0 with an
attribution clause, see `THIRD_PARTY_NOTICES.md`), pinned to tag **1.4.7**.

## Build tags

| Build | Files | Behaviour |
|---|---|---|
| default (`go build ./...`, `CGO_ENABLED=0`, Windows, armv7, ...) | `stub.go` | `Available()` is false, `New` returns `ErrNotAvailable`, `WorkerMain` exits 2 |
| `cgo && rmlines` | `worker_cgo.go`, `cgo_log.go`, `rm_lines_c.h` | real renderer; links `-lrm_lines` |
| `+ rmlines_static` | same | also links the static dependency archives and the C++ runtime |

The client, the wire protocol, extent maths and downsampling (`client.go`,
`protocol.go`, `geometry.go`) are plain Go and build in every configuration.

## Architecture

```
server process                               worker process  (<exe> render-worker)
--------------                               --------------
New(Config) -> client with Config.Workers    WorkerMain(stdin, stdout, stderr)
slots, each lazily spawning a worker           hello frame
RenderFile(ctx, path, opts)                    loop: request -> libMu -> library calls
  pick a free slot (or wait / ctx)                    -> response + pixels
  send request, wait for response
  Options.Timeout / ctx -> SIGKILL worker
  EOF / bad frame       -> SIGKILL, ErrWorkerCrashed
```

- **No library call happens in the server process.** Several library calls
  abort or segfault on bad input (`getFrame` with a short buffer,
  `getSizeTracker` with an unknown layer, `makeRenderer` with a bad tree id,
  `getLayerFull` with an id lacking `:`), and a 24-second single frame was
  observed while fuzzing. A worker crash or hang costs one page, never the
  server.
- `cmd/rmfakecloud/main.go` dispatches `os.Args[1] == "render-worker"` to
  `WorkerMain` before flag or config handling. `Config.WorkerPath` defaults to
  `os.Executable()`. The tests use the test binary itself as the worker
  (`TestMain`).
- **Workers**: `Config.Workers` slots (default 1), one request in flight per
  worker. Idle workers are killed after `Config.IdleTimeout` (default 5 min) and
  respawned on demand. A worker that dies while idle is replaced transparently:
  a reused worker that dies before answering gets one retry on a fresh worker.
- **Errors**: `ErrRender` (bad options, not a v6 file, the library rejected the
  file; the text carries the library's error log; the worker stays alive),
  `ErrWorkerCrashed` (the process died or broke the protocol; the text has the
  exit status and the start and end of its stderr), `ErrTimeout`,
  `ctx.Err()`, `ErrClosed`.

### Defences inside the worker (first line; the subprocess is the second)

- The file must be a regular file of at most 256 MiB that starts with
  `reMarkable .lines file, version=6` (the library cannot read v3/v5).
- `makeRenderer` only ever receives the id `buildTree` just returned.
- `getSizeTracker` only receives `7:1` (the text layer) and ids returned by
  `getLayers`, and those must match `^\d{1,3}:\d{1,20}$`.
- `getFrame` gets a buffer of exactly `w*h*4` bytes. Tracker values that are not
  finite are ignored, the extent is clamped (`Options.MaxExtentMul`, default 4x the
  page per axis), and the supersampled frame must fit `Config.MaxPixels`
  (default 64 Mpx).
- `getConfig` must return `configVersion == 4`; `rm_lines_c.h` has
  `_Static_assert`s on `sizeof(RendererConfig) == 232` and on every field
  offset, so an upstream ABI change fails the build.
- Images go to `addImage` only if every image on the page resolves, through
  `Options.Images`, to a PNG or JPEG that Go can decode (at most 16384 px per
  side). Otherwise images are disabled for the page, because a missing texture
  draws an opaque magenta box. Keys may be the image uuid from `getImageInfo`
  or its `fileName` (`<id>.png`, as stored next to the page on the tablet).
- Every `const char*` the library returns is a `thread_local` buffer: the C
  shims `strdup` it inside the same C call, and Go frees the copy. One global
  mutex (`libMu`) serialises library use, because text rendering shares a
  FreeType singleton. The library's error and info logs are captured for each
  request and returned in the response; debug logs are dropped.
- The worker moves its protocol stream off fd 1 (`dup`, then `dup2(2, 1)`), so
  anything the C++ code prints to stdout lands on stderr instead of corrupting
  frames. The client logs worker stderr through logrus.

### Rendering

1. `buildTree`, `makeRenderer(tree, 0, Options.Landscape)`, `getLayers`.
2. Extent: the union of the size trackers. Every side where ink leaves the page
   gets 30 units of padding (trackers ignore stroke width), the result is
   rounded out to whole units and clamped. It always contains the page.
   `Result.PageW/PageH` is the renderer's frame: the SceneInfo paper size
   (default 1404x1872), swapped when `Landscape` is set.
3. Hidden layers go to `RendererConfig.disabledLayers` (at most 10; any further
   ones are logged and stay visible).
4. **Pass 1 (Ink)**: pens 5 and 18 (highlighters) blacklisted, glyph highlights
   off, text and images on, `Options.Template` applied.
   **Pass 2 (Highlights)**: whitelist {5, 18}, text, images and template off,
   glyph highlights on. The library draws highlights opaque, so they are kept
   apart for a multiply blend in the PDF. With
   `Wikipedia_highlighted_p1.rm` pass 2 holds the glyph highlights and no
   text, and pass 1 has no yellow.
5. Both passes render with `antialias=false` at `Scale*Supersample` (default
   2x). Go then box-downsamples them with premultiplied alpha and converts back
   to straight-alpha NRGBA. The library's own antialias is a 3x3 blur over
   straight alpha that lightens ink edges (mean edge lightening 166 for black
   ink, 0 with supersampling; see `TestEdgeFringe`).

## Wire protocol

Frames are a 4-byte big-endian length followed by the payload.

```
worker -> client   {"hello":"rmlines-render-worker","version":1}
client -> worker   {"path":"/abs/page.rm","options":{landscape,template,scale,supersample,images,imagesOn,maxExtentMul,maxPixels}}
worker -> client   {"pageW","pageH","extent":{L,T,R,B},"inkW","inkH","hasHighlights","error","log","ms"}
                   if error == "": ink frame (inkW*inkH*4 bytes NRGBA), highlights frame (same size, or empty)
```

The client checks every size against the pixel budget before reading. With
`RMLINES_WORKER_DEBUG=1` in the worker's environment, the `debug` request
field enables the test-only ops `abort`, `sleep`, `libaa` and `hideall`.

## Building the library locally

Requirements: CMake >= 3.30, a C++20 compiler (Apple clang 17+, GCC >= 13;
GCC 12 lacks `<format>`), network access at configure time (FetchContent), and
Python 3 with **fontTools**. The library splits its embedded Karla and Libre
Baskerville variable fonts at configure time, and the configure step fails
without fontTools. Use a Release build: a Debug build keeps an `assert` in
`ImageBuffer::allocate`.

Once Track E lands, `make rmlines-lib` (`build/rmlines/build.sh`) does all of
this offline from checksummed sources into `.build/rmlines`. To do it by hand:

```sh
python3 -m venv /tmp/rmlines-venv
/tmp/rmlines-venv/bin/pip install fonttools

curl -L https://github.com/RedTTGMoss/librm_lines/archive/refs/tags/1.4.7.tar.gz | tar xz
cd librm_lines-1.4.7
# 1.4.7 hard-codes a SHARED library; for a static build apply the RM_LINES_STATIC
# patch (build/rmlines/patches/0001-static-library-option.patch), and on musl the
# execinfo guard (0002-musl-execinfo-guard.patch).
patch -p1 < .../0001-static-library-option.patch

cmake -B build -S . -DCMAKE_BUILD_TYPE=Release -DRM_LINES_STATIC=ON \
      -DPython3_EXECUTABLE=/tmp/rmlines-venv/bin/python
cmake --build build --target rm_lines -j8      # about 15 s on an M-series Mac
```

Static outputs, in link order: `build/librm_lines.a`,
`build/_deps/harfbuzz-build/libharfbuzz.a`,
`build/_deps/freetype-build/libfreetype.a` and
`build/_deps/simdutf-build/src/libsimdutf.a`. Without the patch (or with
`-DRM_LINES_STATIC=OFF`) you get `build/librm_lines.dylib` / `.so`.

Never put reMarkable's proprietary fonts in `rm_lines/assets/fonts`: the build
embeds whatever is there.

## Running the tests

Untagged (stub, geometry, protocol, client handshake failure):

```sh
go test ./internal/storage/exporter/rmlines/...
```

Tagged, static (macOS or Linux):

```sh
B=/path/to/librm_lines-1.4.7/build
CGO_ENABLED=1 \
CGO_LDFLAGS="-L$B -L$B/_deps/harfbuzz-build -L$B/_deps/freetype-build -L$B/_deps/simdutf-build/src" \
go test -tags rmlines,rmlines_static ./internal/storage/exporter/rmlines/...
```

The OS-specific link flags are in the `#cgo rmlines_static,darwin` and
`rmlines_static,linux` lines of `worker_cgo.go`: on darwin `-lc++` and the
CoreText, CoreGraphics, CoreFoundation and ApplicationServices frameworks, and
on Linux `-l:libstdc++.a -lm -static-libgcc`. For the dynamic library, drop
`rmlines_static` and add an rpath:
`CGO_LDFLAGS="-L$B -Wl,-rpath,$B"`.

Full-size renders (ink, highlights, and a composite on white) are written to
`$RMLINES_RENDER_DIR` (default `$TMPDIR/rmlines-renders`) for inspection.
The quarter-scale goldens in `testdata/golden` are compared with a tolerance.
To regenerate them after an intended change:

```sh
go test -tags rmlines,rmlines_static -run TestGolden ./internal/storage/exporter/rmlines/ -args -update
```

Benchmarks: `go test -tags rmlines,rmlines_static -run XXX -bench . ./internal/storage/exporter/rmlines/`.
On an M5 Max a page takes 7–52 ms at 1x and 24–187 ms at the default 2x
supersampling (both passes, warm worker).
