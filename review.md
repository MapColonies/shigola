# Code review — `feat/ogc-tiles`

Two-axis review. **Standards** (does it conform to this repo's documented conventions) and **Spec**
(does it faithfully implement the design) were run as independent passes and are reported separately
below. They are deliberately **not merged or reranked** — a change can pass one and fail the other,
and combining them lets one mask the other.

## What was reviewed

| | |
|:---|:---|
| Branch | `feat/ogc-tiles` @ `c81e8ec4` |
| Fixed point | `origin/development` @ `a87f4636` (the exact merge-base) |
| Diff | `git diff origin/development...HEAD -- . ':(exclude)vendor'` |
| Size | 81 files, ~15,971 insertions / 344 deletions (non-vendor) |
| Commits | 22 (`git log origin/development..HEAD --oneline`) |

**Why `development` and not `master`:** ADR-0010 chose `development` as the implementation base, and
`HANDOFF.md` says "Open a PR against `development` when a phase is ready for review." So this is the
diff a reviewer of that PR would see. Reviewing against `master` instead would pull in the layered
cache and WorldCRS84Quad work — 49 commits — which was reviewed separately.

Of the ~16k lines, **14,202 are the two new packages** (`tms/`, `server/ogc/`, 46 files, all new).
The remaining ~1.7k are cross-cutting edits to 16 non-test files plus a new root `tile_grid.go`.

The last two commits (`af5a1c79`, `c81e8ec4`) are documentation-only edits to `docs/ogc-api-tiles.md`
made during a docs pass, not feature work.

## Baseline: what is already green

Do not spend time on these — they were checked before the review and pass:

- **`gofmt -s`** — the branch introduces **zero** violations. (Files elsewhere in the tree are dirty,
  but all pre-date this branch. CONTRIBUTING.md asks that formatting-only changes go in a separate
  PR, so leave them.)
- **`CGO_ENABLED=0 go test -mod vendor ./...`** — passes.
- **`CGO_ENABLED=1 go test -mod vendor -race ./tms/... ./server/... ./cache/... ./atlas/...
  ./config/... ./provider/... ./cmd/...`** — passes.

Both CGO modes are required by CONTRIBUTING.md ("We need to run tests with CGO enabled and
disabled"). Re-run both after any fix.

Backend tests (PostGIS, Redis, S3, HANA) skip unless their `RUN_*_TESTS` env var is exactly `yes`, so
the runs above did not exercise them.

---

## Standards

Sources: `CONTRIBUTING.md` (§ Conventions), conventions inferred from untouched code, plus a fixed
Fowler smell baseline. Tooling-enforced issues (gofmt, vet) excluded.

### Documented-standard breaches (hard)

**S1. Error *type* naming inverts the repo convention.** `CONTRIBUTING.md` § Conventions specifies
`var ErrErrorName = errors.New("provider: canceled")`. The repo extends the `Err*` prefix to error
types: 82 `Err*`-prefixed types repo-wide (`cache/errors.go:5,14,24,33,42`,
`provider/errors.go:17`), and **zero** exported `*Error`-suffixed types outside this diff. `tms`
inverts it:

- `tms/errors.go:36,46,55,65,74,86,114` — `InvalidIdentifierError`, `InvalidZoomError`,
  `TileArgParsingError`, `NoQuadkeySupportError`, `QuadKeyError`, `UnsupportedCRSError`,
  `PointOutsideBoundsError`
- `tms/registry.go:57` — `GridUnavailableError`

> *Verified.* The branch is **internally** inconsistent too: `server/ogc` follows the convention
> (`ErrCollectionNotFound`, `ErrTileSetNotFound`, `ErrUnsupportedFormat` at
> `server/ogc/collection.go:47,60`, `negotiate.go:64`). `tms`'s *sentinel vars* are already correct
> (`ErrNoTransformBackend`, `ErrTMSVersion1`, `ErrGridNotActivated`) — only the struct types invert.
> Renaming is mechanical and touches no behaviour.

**S2. Errors declared outside `errors.go`.** `ErrVariableWidthUnsupported` (`tms/registry.go:29`) and
`GridUnavailableError` (`tms/registry.go:57`) sit outside `tms/errors.go`, unlike every other package
(`cache/errors.go`, `config/errors.go`, `provider/errors.go`).

### Baseline smells (judgement calls)

**S3. Duplicated Code — tile-bounds validation, four copies.** The same "MatrixSize → bound x and y
independently → error" shape at `cache/cache.go:300-310`, `server/handle_map_layer_zxy.go:67-78`,
`server/ogc/handle_tile.go:148-163`, `cmd/shigola/cmd/cache/seed_purge.go:198-206`. Extract one
`grid.ValidateTile(z, x, y)`. This is the highest-value item on this axis: per-axis validation is
load-bearing for the non-square grids, and four copies is four chances to drift.

**S4. Duplicated Code — the per-request grid is smuggled as hidden state.** Both
`server/ogc/handle_tile.go:60-61` (`m.TileMatrixSets = []*tms.TileMatrixSet{grid}`) and
`cmd/shigola/cmd/cache/worker.go:23-30` (`bindRunGrid`) mutate a copy of `Map` to communicate which
grid this call should use, rather than passing it to `Encode`. Two independent sites inventing the
same workaround suggests `Encode` wants the grid as a parameter.

**S5. Duplicated Code — link base path.** `base := []string{"collections", c.ID, "tiles", grid.ID()}`
at `server/ogc/handle_collections.go:207,222` and `server/ogc/tilejson.go:37`.

**S6. Speculative Generality — ~40 unused exported `tms` symbols.** `Quadkey`, `QuadkeyToTile`,
`Neighbors`, `Parent`, `Children`, `Feature`/`FeatureOptions`, `ZoomForRes`, `MinMax`, `IsValid`,
`LngLat`, `XY`, `BBox`, `Registry.Replace`, `BundledIDs`, `LoadDefinition`, `LoadGrid`, plus four
unused error types — no callers outside the package. ~1,300 lines carried for 3 active grids.
**Weigh against ADR-0009 before deleting:** it mandates a *faithful* morecantile port including its
test suite, and fidelity is the stated correctness oracle. Removing surface may be a spec violation
rather than a cleanup. Recommend leaving unless the spec is amended.

**S7. Mysterious Name — four near-synonymous registry lookups.** `Registry.List` / `Registered` /
`Available` / `AvailableIDs` (`tms/registry.go:180-236`) differ materially (servable vs all vs
WebMercatorQuad-first) but not nominally.

**S8. Middle Man.** `func logf(...) { log.Errorf(...) }` — `server/ogc/handlers.go:13`.

**S9. Divergent Change.** `cmd/shigola/cmd/cache/seed_purge.go` gains grid resolution, bounds-SRID
validation, *and* `validateTileInGrid` — the last used only by `tile_list.go:131` and
`tile_name.go:55`.

**S10. Data Clumps.** `cache.NewKey(grid, mapName, layerName, z, x, y)` (`cache/cache.go:365`); two of
four call sites pass `""` for layer.

**S11. Minor.** `MediaTypeTileJSON` is byte-identical to `MediaTypeJSON` (`server/ogc/negotiate.go:16`);
`Map.TileGrid` (`atlas/map.go:99`) re-implements `TileGrids`' first-non-nil scan; `atlas/map.go:53`
panics on a request path.

### S12. `tile_srid` is removed with no warning — read this carefully

The Standards pass flagged the `tile_srid` TOML key as deleted with no deprecation. **Relative to the
`development` base this is accurate**, and the silence is real: `config/config.go:371` discards the
TOML decoder's metadata —

```go
// decode conf file, don't care about the meta data.
_, err = toml.NewDecoder(reader).Decode(&conf)
```

— which is where `Undecoded()` would report unknown keys. So a config still setting `tile_srid` loads
without complaint and silently falls back to the default grid. That unknown-key silence is
pre-existing tegola behaviour, not introduced here.

**Two qualifications before acting:**

1. **`tile_srid` never shipped on `master`.** It was introduced on `feat/world-crs84quad-tiles`,
   carried into `development`, and replaced here — all pre-release. Verified: zero occurrences on
   `master`/`origin/master`. So no released config can contain it; only someone who ran the fork's
   own `development` branch is affected.
2. **The doc removal was deliberate,** not an oversight. `docs/ogc-api-tiles.md` no longer mentions
   `tile_srid` because commit `c81e8ec4` removed the mention on purpose — describing a migration from
   a key that never shipped was judged misleading. Do not "fix" this by reinstating it.

**Suggested action:** a startup warning when a map declares `tile_srid`, pointing at
`tile_matrix_sets`. Cheap, and it converts a silent behaviour change into a visible one for
development-branch users. Do **not** reinstate the doc section.

---

## Spec

Sources: the planning repo `NivGreenstein/tegola-claude-planning` @ `origin/feat/ogc-tiles` —
`architecture.md`, `design.md`, `HANDOFF.md`, `CONTEXT.md`, `docs/adr/0001..0010`.

### Verified as correctly implemented

ADR-0004's 11-class conformance list (`server/ogc/types.go:56-71`); ADR-0002 collection ids and
resolution (`server/ogc/collection.go:15,74-116`); ADR-0005 `?f=` + `Accept` + the `pbf` alias
(`server/ogc/negotiate.go:47,89-127`); ADR-0007 key shape threaded through every tier via `String()`
(`cache/cache.go:340-395`); ADR-0003 viewer move (`server/viewer_embed.go:16-42`); ADR-0006
`tileMatrixSetLimits` emitted (`server/ogc/handle_collections.go:230,287-325`); ADR-0009's 13 grids /
3 active (`tms/registry.go:41-51`).

### (a) Missing or partial

**P1. `/capabilities` was never made TMS-aware.** ADR-0006: *"The existing WebMercator-only TileJSON
handler must become TMS-aware (it currently hardcodes the WebMercator scheme), so a WorldCRS84Quad
tileset is described correctly."* `server/handle_map_capabilities.go` is **untouched by the diff**
(verified) — it still emits `Scheme: SchemeXYZ` with no `crs` (`:55-59`, `:129`, `:156`). A map whose
default is WorldCRS84Quad is misdescribed on the native capabilities endpoint. Only the new
`server/ogc/tilejson.go` is TMS-aware. **Spec is right, implementation is partial.** Highest-value
item on this axis.

**P2. `/api` skips content negotiation.** ADR-0005: *"On the new OGC API routes, representation is
chosen by an `f` query parameter … with `Accept` as fallback."* `HandleAPI` never calls `negotiate`
(`server/ogc/api.go:47-80`) — it writes `MediaTypeOpenAPI` unconditionally (verified). So
`/api?f=nonsense` returns 200 where every other route returns 400. Inconsistent with the rule the
same ADR applies everywhere else.

**P3. `TileMatrixSet` and `Registry` are concrete, not interfaces.** ADR-0008 / design.md:15 specify
*"a `TileMatrixSet` interface + a `Registry`"*, *"`tms` registry behind an interface"*. Both are
structs (`tms/tilematrixset.go:47`, `tms/registry.go:77`). The factory (`registry.go:104`) does
preserve the load-bearing "adding a grid never changes a call site" property, so this is a **deviation
in form, not in effect**. Consider amending the ADR rather than the code.

**P4. No `tms/testdata/`.** design.md:65 asks for `testdata/` alongside the ported tests; fixtures are
inlined instead. Cosmetic.

### (b) Not asked for (scope creep)

**P5. `--bounds-srid` narrowed, exported helpers deleted.**
`cmd/shigola/cmd/cache/seed_purge.go:330-347` restricts `--bounds-srid` to 4326 and deletes exported
`IsKnownSrcConversionSRID` / `AvailableSrcConversions`; enumeration switched to `grid.Tiles(...)`
(`:429`), changing tile-boundary inclusion. ADR-0007 asked only that *"`cache seed`/`purge` commands
must take a `--tile-matrix-set` (TMS) argument"*, and ADR-0001 sanctioned exactly **one**
non-additive change (the landing page). This is a second and third. Decide: accept and record in the
ADRs, or restore.

**P6. CITE automated in CI.** `.github/workflows/ogc_cite.yml` + `.github/cite/` (~310 lines).
design.md Phase 3 asked to *run* the suite until `/conformance` passes — not to automate it. Harmless
and arguably valuable, but unrequested; record it.

### (c) The spec is wrong and the code is right — fix the ADRs, not the code

These belong in the **planning repo**, not here.

**P7. ADR-0009 misclassifies the gated grids.** It says the variable-width
`GNOSISGlobalGrid`/`CDB1GlobalGrid`/`LINZAntarticaMapTilegrid` *"register only when a transform
backend is present."* Wrong twice: GNOSIS and CDB1 are EPSG:4326, so the identity transformer exists
and the ADR's own rule would **activate** them; LINZ is EPSG:5482 and is not variable-width. The code
adds a separate `ErrVariableWidthUnsupported` (`tms/registry.go:29,345-356`) — correct.

**P8. ADR-0007's key notation contradicts architecture.md.** ADR-0007 writes
`{…, TileMatrix(z), TileRow(y), TileCol(x)}` (row before col); `architecture.md:185` says
`{…, Z, X, Y}`. The code is Z,X,Y (`cache/cache.go:365-395`). ADR-0007's notation is the error.

**P9. ADR-0009's optional WorldMercatorWGS84Quad was not taken** — it gates on
`ErrNoTransformBackend` (`tms/transform.go:133-143`). Permitted by the ADR; mark it resolved.

### Implementation defect

**P10. `cache.NewKey` panics on a nil grid instead of erroring.** `cache/cache.go:365-378` documents
*"grid is required, and deliberately not defaulted … ParseKeyForGrid rejects nil for the same
reason"*, then calls `grid.ID()` — and `func (t *TileMatrixSet) ID() string { return t.def.ID }`
(`tms/tilematrixset.go:136`) has no nil guard (verified). The comment's intent is right; the code
nil-derefs. Either return an error or make `ID()` nil-safe.

---

## Summary

- **Standards — 12 findings.** 2 hard (S1, S2: error naming/placement, mechanical to fix). Worst:
  **S3**, tile-bounds validation duplicated across four sites — per-axis validation is what makes the
  non-square grids safe, and four copies is four chances to drift.
- **Spec — 10 findings.** 4 partial/missing, 2 scope creep, 3 spec-is-wrong, 1 defect. Worst:
  **P1**, `/capabilities` was never made TMS-aware, so a WorldCRS84Quad map is misdescribed on the
  native endpoint — an explicit ADR-0006 requirement that no commit addressed.

No single worst issue across both axes: that is the reranking the two-axis separation exists to
prevent.

### Suggested order for the fixing agent

1. **P10** — one-line nil guard, removes a panic.
2. **P2** — route `/api` through `negotiate`, closing the ADR-0005 gap.
3. **P1** — the real feature gap; needs a decision on whether `/capabilities` gains a `crs`/scheme or
   is documented as WebMercator-only.
4. **S1 + S2** — mechanical rename and file move; do them together, in their own commit, since they
   touch many lines and no behaviour.
5. **S3, S4, S5** — the three duplications, in that order.
6. **S12** — add the `tile_srid` startup warning. **Do not** reinstate the removed doc section.
7. **P7, P8, P9** — corrections to the ADRs in the *planning* repo, not this one.
8. **P5, P6** — decisions for the author, not fixes: accept the extra non-additive changes and record
   them in the ADRs, or revert.

Leave **S6** alone unless ADR-0009's fidelity requirement is amended first.

After any change, re-run both CGO modes (see *Baseline* above); CONTRIBUTING.md requires both.
