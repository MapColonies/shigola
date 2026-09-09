# Contributing to Shigola

Shigola is a Go vector tile server that serves [OGC API - Tiles](https://ogcapi.ogc.org/tiles/).
Thank you for thinking about contributing — this guide assumes a basic working knowledge of Git and
Go, and covers how to get a change reviewed and merged.

> **Provenance.** Shigola began as a fork of [Tegola](https://github.com/go-spatial/tegola), and most
> of this codebase still descends from it under the MIT licence. `LICENSE.md`, `NOTICE.md`, the
> copyright notice and the inherited `CHANGELOG.md` history stay as they are: that attribution is a
> licence obligation, not a leftover, and it is never removed as part of a cleanup.
>
> It is no longer a fork that tracks upstream, and it is **not a superset of Tegola**. Four
> behaviours changed incompatibly — `/` is the OGC landing page, cache keys gained a leading
> `{tileMatrixSetId}`, the binary was renamed, and metrics went from `tegola_*` to `shigola_*` — and
> rather more was removed than added: the viewer, the native `/maps/...` tile routes, and the
> GeoPackage, SAP HANA and standard `postgis` providers. Behaviour this fork kept and did not change
> is upstream's, and [tegola.io](https://tegola.io) still documents that part.
>
> **Every change belongs here.** There is no upstream to forward a generally useful fix to any more,
> and no reason to keep one out of this tree.

## Where things live

| | Where |
|:---|:---|
| The server | [MapColonies/shigola](https://github.com/MapColonies/shigola) — this repository |
| The docs site | [MapColonies/shigola-docs](https://github.com/MapColonies/shigola-docs), published to [GitHub Pages](https://mapcolonies.github.io/shigola-docs/) |
| Released binaries | the [releases page](https://github.com/MapColonies/shigola/releases) |

## Found a bug, or want a feature?

Everything starts with an [issue](https://github.com/MapColonies/shigola/issues). Search the open
ones first: if something close already exists, add what you know to it rather than opening a
duplicate — and if you have nothing to add, a 👍 is a useful signal on its own.

* File a bug with the [bug template](https://github.com/MapColonies/shigola/issues/new?template=bug.md).
  For anything about the tiles that come back, include the configuration file and enough of the data
  to reproduce it — a tile that looks wrong is not reproducible without the rows behind it.
* File a feature request with the
  [feature template](https://github.com/MapColonies/shigola/issues/new?template=feature.md), and say
  what the use case is, not only what the feature is.

The issue is where the design gets discussed, so keep the discussion there rather than in a chat
channel — and if a decision does get made somewhere else, write it back onto the issue. A pull
request that fixes a bug or adds a feature references its issue.

**Security issues do not go in a public issue.** Report a suspected vulnerability privately through
this repository's GitHub security advisories (*Security* → *Report a vulnerability*), or by
contacting a maintainer directly.

Everyone taking part is governed by the [Code of Conduct](CODE_OF_CONDUCT.md); report unacceptable
behaviour to this repository's maintainers.

## How work is tracked

MapColonies tracks work on Shigola in the **MAPCO** Jira project, and a pull request title carries
its issue key in parentheses — that is what makes the ticket findable from the pull request list and
from the squashed commit left on `master`. Contributors without access to that tracker should
reference the GitHub issue number instead; nothing here requires a Jira account.

## Making a change

`master` is always the most recent state of the code, and pull requests are opened against it. There
is no release-candidate branch.

* **Never commit or push to `master`.** Branch as `<type>/<slug>` — `fix/cache-histogram-buckets`,
  `feat/ogc-tiles` — push the branch, and open a pull request.
* **Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):**
  `<type>(<optional scope>): <subject>`, where type is one of `feat`, `fix`, `docs`, `chore`,
  `refactor`, `test`, `ci`, `perf`, `build`, `style`, `revert`. Subject in lowercase imperative, no
  trailing period.
* **Pull request titles use the same grammar and end with the issue key:**
  `feat(server): remove the embedded viewer (MAPCO-11482)`.
* **One logical change per commit.** Formatting-only churn goes in its own commit *and* its own pull
  request — it keeps engineering changes reviewable separately from reflowed whitespace.
* **The pull request body carries the reasoning**, not a list of files: what changed and why, how it
  was verified, and anything deliberately left out.
* **Update the docs in the same piece of work.** `shigola-docs` pages are derived from sources here,
  and this repository is the source of truth. A change to any of these needs the matching docs page
  updated, as a separate branch and pull request in `shigola-docs`, with the two cross-referencing
  each other:

  | Docs page (`shigola-docs/`) | Source (here) |
  |:---|:---|
  | `docs/ogc-api-tiles.md` | `docs/ogc-api-tiles.md` |
  | `docs/tile-matrix-sets.md` | `docs/ogc-api-tiles.md`, `tms/doc.go`, `tms/registry.go` |
  | `docs/layered-cache.md` | `README.md` § "Layered cache" |
  | `docs/configuration.md` § Redis | `cache/redis/README.md` |
  | `docs/tracing.md`, `docs/configuration.md` § Tracing | `tracing/README.md` |
  | `docs/logging.md` | `internal/log/log.go`, `tracing/README.md` § "Correlating logs with traces" |

Once the pull request is open a maintainer reviews it and may ask for changes. Keep it up to date as
other work lands on `master` ahead of yours.

### Not sure where to start?

Look through the issues for one that interests you, and say on it that you are picking it up so two
people do not write the same patch. An issue labelled `good first issue` is one a maintainer thinks
is a reasonable place to start, but it is not the only place. If you are unsure how to approach one,
ask on the issue.

## Building from source

`vendor/` is committed, so **always build and test with `-mod vendor`** — every command in this
repository does.

```bash
git clone https://github.com/MapColonies/shigola
cd shigola

go build -mod vendor ./cmd/shigola
./shigola serve --config=path/to/config.toml
```

`internal/build/*.generated.go` records which build tags a binary was built with. It is generated,
never hand-edited:

```bash
cd internal/build && go generate
```

Optional features compile out behind `noS3Cache`, `noRedisCache`, `noAzblobCache`, `noGCSCache`,
`noPostgisProvider` and `noPrometheusObserver`; `pprof` opts in.

## Code conventions

* **`gofmt -s`.** If running it produces changes in parts of the tree you are not working on, send
  those in a separate pull request.
* **Error variables** take the form `var ErrErrorName = errors.New("provider: canceled")` — the text
  all lowercase, with no punctuation at the end.
* **Table-driven subtests keyed by name**, with a `fn := func(tc tcase) func(*testing.T)` closure.
  The pattern is uniform across the repository; match it rather than inventing a variant.
  `cache/memory/memory_test.go` is a short example.
* **Comments here carry design rationale**, including ADR references (`ADR-0003`, `ADR-0007`, …) and
  why an alternative was rejected. Preserve that when editing near them, and write new comments the
  same way — a comment that only restates the code is not worth the line.
* **Environment variables are `SHIGOLA_*`**, falling back to `TEGOLA_*` with a deprecation warning
  (`internal/env/getenv.go`). Read them through `env.Getenv("OPTIONS")`, never `os.Getenv`.
* **Anything new that is configurable is wired in two places:** `config/` parses and validates it,
  and `cmd/internal/register/` turns the parsed config into something registered on the atlas. A
  setting wired in only one of them is silently inert.

## Testing

### Running the suite

Most of the suite needs the PostGIS and Redis fixtures. `docker compose up -d` returns as soon as the
containers start rather than when the fixture is loaded, so wait for the one-shot `migration` service
the way CI does — otherwise the tests run against a half-restored database and blame the server for
it:

```bash
docker compose up -d
docker wait migration        # must print 0 before going on

go test -mod vendor -race ./...
docker compose down
```

**One test mode, not two.** Nothing in this tree is compiled conditionally on cgo, so `CGO_ENABLED`
no longer changes what is built or what is tested, and `internal/build` fails if that stops being
true. Leave it unset: `go test -race` links a C runtime for its detector and needs cgo available.
That the tree still *builds* without a C toolchain is checked separately, and CI checks it:

```bash
CGO_ENABLED=0 go build -mod vendor ./...
CGO_ENABLED=0 go test -run '^$' -mod vendor ./...   # links every test binary, runs none
```

Two more gates worth running before you push:

```bash
gofmt -s -l . | grep -v '^vendor/'    # vendor/ is never -s clean; nothing else may appear
govulncheck ./...
```

### Opt-in suites

Provider- and backend-specific tests **skip unless you opt in** (`internal/ttools.ShouldSkip`), so a
green `go test ./...` may have run almost nothing for those packages. Each gate takes the literal
string `yes`:

| Gate | Also needs |
|:---|:---|
| `RUN_POSTGIS_TESTS` | `PGURI`, `PGURI_NO_ACCESS`, `PGSSLMODE` |
| `RUN_REDIS_TESTS` | — |
| `RUN_DATA_TESTS` | the fixture stack; see below |
| `RUN_S3_TESTS` | `AWS_TEST_BUCKET`, `AWS_REGION` — reaches a real bucket, so not reproducible locally |
| `RUN_AZBLOB_TESTS` | — |

`.github/workflows/on_pr_push.yml` has the exact values CI uses.

### OGC conformance

`.github/cite/run.sh <tileMatrixSetId> <tileMatrix> <tileRow> <tileCol>` runs the OGC CITE suite in
TeamEngine against a running server. It is the only check here that catches conformance rules the
code cannot check about itself, so CI runs it on changes to the server, `tms/`, `atlas/`, the PostGIS
provider and its fixtures, and weekly. It serves the Athens layers out of the PostGIS fixture through
`mvt_postgis`, so bring the fixture up first.

### Tile-content checks

`RUN_DATA_TESTS=yes` runs the black-box tile-content checks in `server/tilecontent/`. They stand the server up on
a real listener, ask it for tiles over HTTP, and assert what comes back down to exact tile-space
coordinates — the one thing no other check here does. `TestMVTProviders` stops at the provider, and
the OGC CITE suite checks conformance rather than content: its runner notes that it passes against an
empty tile.

They have their own gate rather than riding on `RUN_POSTGIS_TESTS` so that exactly one CI job runs
them, and a failure names itself instead of being one line inside a job that fails for many reasons.
They need the same fixture stack as everything else:

```bash
docker compose up -d
docker wait migration        # must print 0 before going on

RUN_DATA_TESTS=yes \
  PGURI="postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable" \
  PGSSLMODE=disable \
  go test -mod vendor ./server/tilecontent/
```

They live in a package of their own so CI can name the whole set without naming the tests in it: the
job runs this package, so a check added here is covered the day it lands.

The expected tiles are pinned two ways at once. Golden files under `server/tilecontent/testdata/golden/` hold the
whole decoded tile and catch changes nobody thought to assert; `-update-golden` rewrites them. A
second set of assertions is written literally in the test and does **not** move when a golden is
rewritten, so a golden regenerated in error still fails.

That split is the only thing keeping the goldens honest, and it works because they are small — 45
lines across six files, over two fixtures of four and eleven features. Read the diff when you
regenerate one. If a golden ever grows past what a reviewer will actually read, it has stopped being
an assertion: the Athens fixture next door would put 633 features and about 4300 coordinate pairs in
a single one.

Three fixtures, because they answer different questions. `postgis-scheme-edges.sql` is four points
placed at the poles, the antimeridian, a tile corner and the shallowest zoom — where the two schemes
differ in shape rather than only in extent.
`postgis-tile-content.sql` is four layers and eleven features around one WorldCRS84Quad tile, with
one column of each MVT value type, a null attribute, a road clipped at the tile edge, simple and
holed polygons, a feature outside the tile and a layer outside it entirely.
`postgis-cross-crs.sql` is three points on one meridian at general latitudes, served through both a
4326 and a 3857 layer over the same table: what a tile must not depend on is which SRID the geometry
was stored in, and that is an assertion the other two cannot make.

Two things about that second fixture are worth knowing before you add to it. `ST_AsMVTGeom`'s buffer
defaults to **256**, not 0, so geometry legitimately runs past the extent — but that buffer decides
*clipping*, not *selection*: which rows reach it at all is settled earlier by the layer SQL's
`WHERE geom && !BBOX!` against the unbuffered envelope. And the tile's column is larger than the
scheme's row count on purpose: reading the path's row and column the wrong way round then asks for a
row that does not exist, which is a rejection rather than a plausible-looking tile.

### Coverage floor

CI fails a build whose total statement coverage falls below the floor recorded in
[`ci/coverage-baseline.txt`](ci/coverage-baseline.txt). That file is the only place the number
lives — read the `floor` line for the current value. It is a floor, not a target: it exists to stop
a silent slide, not to describe where coverage ought to be, and raising it is a deliberate decision
to make once the coverage is really there.

To run the same check locally you need the fixture services up, because the baseline is measured with
the PostGIS and Redis suites enabled. `docker compose up -d` returns as soon as the containers
start, not when the fixture is loaded, so wait for the one-shot `migration` service the way CI does —
otherwise the suite runs against a half-restored database and measures nonsense:

```bash
docker compose up -d
docker wait migration        # must print 0 before going on

RUN_POSTGIS_TESTS=yes RUN_REDIS_TESTS=yes \
  PGURI="postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable" \
  PGURI_NO_ACCESS="postgres://shigola_no_access:postgres@localhost:5432/shigola?sslmode=disable" \
  PGSSLMODE=disable \
  go test -mod vendor -race -covermode atomic -coverprofile=profile.cov ./...

go run -mod vendor ./ci/coverage        # check the profile against the floor
```

`-race` alongside `-covermode atomic` is not redundant: atomic is race-safe *counting*, not race
*detection*, and the cache write path runs a goroutine pool, detached contexts and a concurrent tier
fan-out that only `-race` inspects.

Running with fewer gates enabled measures less, so the check may fail locally on a tree that is fine
in CI. Compare the per-package rows in the baseline rather than only the total.

The baseline deliberately records **only** the gates a contributor can provision from this
repository. `RUN_S3_TESTS` is enabled in CI and does really run there — as of this writing it
measures `cache/s3` at 65.6% — but it is not reproducible locally: it reaches an S3 bucket this
repository does not provision and a contributor has no way to stand up. A baseline nobody can
regenerate is not a baseline, so it is left out.

The consequence is worth being clear about: `cache/s3` is recorded near zero in the baseline, so
**CI measures a good deal higher than the recorded total**. That is the safe direction for a floor.
It also means the baseline still carries rows for packages the provider-removal work has since
deleted; it is a before-picture on purpose, and those rows are what the removals are measured
against.

To regenerate after a change that is *meant* to move the numbers, run `-write` in the same shell that
ran the tests — it records the `RUN_*_TESTS` gates it finds set, so the baseline says which suites
its numbers came from:

```bash
go run -mod vendor ./ci/coverage -write
```

Regenerating keeps the recorded floor unless you pass `-floor` — lowering it is meant to be an
explicit edit you justify in the pull request, not a side effect of running `-write` on a machine
with fewer services running.
