# reMarkable `.rm` test fixtures

Shared fixtures for the renderer and exporter tests. Go ignores `testdata` directories when building.

| Directory | Source | Licence |
|---|---|---|
| `v6/` | `tests/draw_files`, `tests/files`, `tests/color_files` of [LIB rMLines](https://github.com/RedTTGMoss/librm_lines) at tag 1.4.7 | Apache-2.0 with attribution clause (see `THIRD_PARTY_NOTICES` in the repo root once added) |
| `v5/` | `encoding/rm/test_v3.rm`, `test_v5.rm` from [juruen/rmapi](https://github.com/juruen/rmapi) v0.0.25 | AGPL-3.0 |
| `images/` | `tests/images` of LIB rMLines; referenced by `Test_Images.rm` | Apache-2.0 with attribution clause |

File names have spaces, commas and ampersands replaced with underscores. All `v6/` files are `reMarkable .lines file, version=6`; `v5/` holds one version 3 and one version 5 file. Device-captured fixtures (Paper Pro notebooks with their `.content` files and the tablet's own PDF exports) go under `device/` when available and must not be committed without permission from their author.
