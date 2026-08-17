# Third-party notices

Shigola redistributes and derives from the following works. Each is used under
its own license, reproduced or linked below.

---

## Tegola

**Shigola is a fork of Tegola.** Effectively all of this codebase originates
there.

- Project: <https://github.com/go-spatial/tegola>
- Documentation: <https://tegola.io>
- Authors: the Go Spatial team and Tegola's contributors
  (<https://github.com/go-spatial/tegola/graphs/contributors>)
- License: MIT — see [LICENSE.md](LICENSE.md), which retains the original
  copyright notice
- Copyright: (c) 2016 The Tegola Authors

Shigola renames the project, adds an OGC API - Tiles surface, a TileMatrixSet
registry and a layered cache, and makes no commitment to remain compatible with
Tegola. Everything else is Tegola's work.

This repository's git history was squashed to a single commit when the fork was
renamed, so Tegola's 1,272 commits and their per-commit authorship are not
present here. That record remains at <https://github.com/go-spatial/tegola>.
Tegola's release history is kept verbatim in [CHANGELOG.md](CHANGELOG.md), and
its contributors are credited in [CONTRIBUTORS.md](CONTRIBUTORS.md).

---

## morecantile

The [`tms`](tms/) package is a **faithful Go port of morecantile**, not merely
inspired by it. Its OGC TMS 2.0 document model, tile algorithms, the thirteen
bundled grid definitions under [`tms/data/`](tms/data/), and its test suite were
all translated from the Python original, and morecantile's golden values serve
as the port's correctness oracle.

- Project: <https://github.com/developmentseed/morecantile>
- Version ported: 7.0.3
- Copyright: (c) 2020 Development Seed
- License: MIT — reproduced in full at
  [`tms/LICENSE-morecantile`](tms/LICENSE-morecantile)

---

## Vendored Go dependencies

Third-party Go modules are vendored under [`vendor/`](vendor/). Each retains its
own license file within its module directory; see [`go.mod`](go.mod) for the
full list and versions. These include, among others:

- `github.com/go-spatial/geom`, `github.com/go-spatial/proj`,
  `github.com/go-spatial/cobra` — Go Spatial, MIT
- `github.com/dimfeld/httptreemux` — MIT

---

## Standards and test suites

- **OGC API - Tiles** and the **Two Dimensional Tile Matrix Set** standard are
  publications of the [Open Geospatial Consortium](https://www.ogc.org/).
- Conformance is verified with OGC's
  [CITE](https://cite.opengeospatial.org/) `ets-ogcapi-tiles10` executable test
  suite.
