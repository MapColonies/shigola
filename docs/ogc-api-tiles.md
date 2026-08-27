# OGC API - Tiles

shigola serves [OGC API - Tiles](https://ogcapi.ogc.org/tiles/) for vector (Mapbox Vector Tile)
data. It is the only tile surface: the native `/maps/...` routes it was once additive to have been
removed.

## Upgrading — two breaking changes

**1. The service root is the landing page.**

OGC API - Tiles requires a landing page at the service root, so a request for `/` returns the JSON
landing page. Update any bookmark, reverse-proxy rule or health check that pointed at `/`.

An unknown path returns 404.

**2. The cache key format changed — purge and re-seed.**

Cache keys now begin with the tiling scheme:

```
before   {map}/{layer}/{z}/{x}/{y}
after    {tileMatrixSetId}/{map}/{layer}/{z}/{x}/{y}
```

Without it, tiles in two schemes collide: WorldCRS84Quad's matrix is twice as wide as
WebMercatorQuad's, so the same `z/x/y` names different ground in each, and one scheme's tiles would
be served for the other's.

Existing entries are not wrong, just unreachable — nothing reads the old keys. Purge them and
re-seed:

```sh
shigola cache purge --config=config.toml --bounds=-180,-85.0511,180,85.0511 --max-zoom=…
shigola cache seed  --config=config.toml --bounds=-180,-85.0511,180,85.0511 --max-zoom=…
```

For a file or S3 cache, deleting the old directory tree is faster than purging tile by tile.

## Configuration

`tile_matrix_sets` names the tiling schemes a map may be requested in. It is optional, and no
released configuration key is replaced by it — a config that omits it serves WebMercatorQuad, as
before:

```toml
[[maps]]
  name = "parks"
  # Omit for every scheme this build serves. The first entry is the map's
  # default: WebMercatorQuad.
  tile_matrix_sets = ["WebMercatorQuad", "WorldCRS84Quad"]
```

Schemes are configured per map, not per layer. A map's layer-collections offer exactly the schemes
their map does.

A map's tileset metadata is served per scheme, at
`/collections/{collectionId}/tiles/{tileMatrixSetId}`. Asking for it as TileJSON (`?f=tilejson`) adds
Shigola's `crs` and `tileMatrixSetId` extension members, since TileJSON itself assumes WebMercator
and defines neither.

This build serves the schemes that need no coordinate transformation backend:

| tileMatrixSetId | CRS | Axis order | Matrix at zoom z |
|---|---|---|---|
| `WebMercatorQuad` | EPSG:3857 | easting, northing | 2^z × 2^z |
| `WorldCRS84Quad` | OGC:CRS84 | longitude, latitude | 2·2^z × 2^z |
| `WGS1984Quad` | EPSG:4326 | latitude, longitude | 2·2^z × 2^z |

The two geographic grids have the **same matrix shape** and index the same ground; they differ only
in the axis order their CRS declares, which is how each states its `pointOfOrigin` — `[-180, 90]` for
CRS84 against `[90, -180]` for EPSG:4326. `tms.matrixOrigin` swaps the inverted pair, and
`TestWGS1984QuadInvertedAxes` pins the two to identical extents on every tile through zoom 3.

The other schemes in the OGC register ship with the build but are not servable: they need a
projection backend that is not wired up. `/tileMatrixSets` lists only what can be served.

## Endpoints

| Path | Resource |
|---|---|
| `/` | Landing page |
| `/api` | This service's OpenAPI 3.0 definition |
| `/conformance` | The conformance classes implemented |
| `/collections` | Every collection |
| `/collections/{collectionId}` | One collection |
| `/collections/{collectionId}/tiles` | The collection's tilesets, one per scheme |
| `/collections/{collectionId}/tiles/{tileMatrixSetId}` | Tileset metadata (`?f=tilejson` for TileJSON 3.0) |
| `/collections/{collectionId}/tiles/{tileMatrixSetId}/{tileMatrix}/{tileRow}/{tileCol}` | A vector tile |
| `/tileMatrixSets` | The tiling schemes served |
| `/tileMatrixSets/{tileMatrixSetId}` | One scheme's definition |

### Which build is answering

Two resources report the running build, so an operator can tell from the service itself:

```
GET /       ->  { "title": "shigola", "shigolaVersion": "v1.2.3", ... }
GET /api    ->  { "info": { "version": "1.0.0", "x-shigola-version": "v1.2.3", ... } }
```

Both are extension members. OGC API - Common defines no place for an implementation's version, and
OpenAPI's `info.version` is the version of the *API* — fixed by the specification this surface
implements, and unmoved by a rebuild — so the build is reported alongside it as an `x-` extension
rather than in it. Neither member appears when the binary was built without a version stamped in.

The removed `/capabilities` endpoint used to be the only place this was reported.

### No style document

Shigola serves no style document. The `/maps/{map}/style.json` endpoint that generated a Mapbox GL
style is gone (MAPCO-11485), and nothing replaces it: styling is a separate specification — OGC API -
Styles — which this server does not implement. A client brings its own style and points its vector
source at a tileset's TileJSON:

```json
"sources": {
  "parks": {
    "type": "vector",
    "url": "http://localhost:8080/collections/parks/tiles/WebMercatorQuad?f=tilejson"
  }
}
```

This is a deliberate omission, not a gap: a generated style guessed colours from layer names, which
is not something a tile server is better placed to decide than the client rendering it.

### Collections

Every map is a collection, and so is every layer of every map:

```
parks           the whole map — tiles carry all its layers
parks:trees     one layer — tiles carry only that layer
```

The map-collection is always published, even for a single-layer map, so a map name is always a
usable collection id.

### Tile paths are z/y/x

OGC orders a tile path `{tileMatrix}/{tileRow}/{tileCol}` — zoom, **row**, then **column**. This is
transposed from the `z/x/y` order of the removed `/maps/{map}/{z}/{x}/{y}` routes, which is the one
thing to get right when moving a client across:

```
was    /maps/parks/3/5/2                                  z=3 x=5 y=2
now    /collections/parks/tiles/WebMercatorQuad/3/2/5      z=3 y=2 x=5   — the same tile
```

Rows and columns are validated separately, so a transposed request is rejected rather than served
as a different tile — in WorldCRS84Quad at z1 there are four columns but only two rows.

### Content negotiation

`?f=` selects a representation and overrides `Accept`. An unrecognised `f` is a 400 rather than a
fallback, so a typo does not quietly return something else. An `Accept` header naming only types
this service cannot produce gets the default representation, which is what a browser receives.

| resource | accepted `f` |
|---|---|
| a tile | `mvt`, or `pbf` for the same thing |
| tileset metadata | `json` (default), `tilejson` |
| everything else | `json` |

`/api` falls under that last row. It has only one representation, but `?f=` is still checked against
it, so `/api?f=html` is a 400 like anywhere else. Its body is served as
`application/vnd.oai.openapi+json;version=3.0` rather than plain `application/json` — `json` names
the representation, and for this resource that representation is an OpenAPI 3.0 definition, which
OGC requires carry the specific media type.

`mvt` is canonical: it is what every link and template this service emits says, and it is the name
in the OGC conformance class. `pbf` is accepted because that is what the removed `/maps/...` routes
called the same tile, serving it at a `.pbf` extension, and it is what the `format` member of the
TileJSON above says — being refused for using our own word for it would be surprising. Matching ignores
case. The alias resolves to MVT before a resource's own formats are consulted, so `?f=pbf` on a
JSON-only resource is still a 400.

## Caching

Tile requests use the same cache keys `shigola cache seed` writes, so a seeded tile is served rather
than generated a second time. The key is
`{tileMatrixSetId}/{map}/{layer}/{z}/{x}/{y}` — it does not include the query string, so every
spelling of `?f=` shares one entry rather than storing the same bytes twice.

A tile request carrying any *other* query parameter is served **uncached**: the key cannot describe
it. Nothing on this surface passes query parameters through to a provider — `[[maps.params]]` is
still configurable but unread, since the route that consumed it was the removed per-layer tile route
— so no such request can reach a different rendering. Serving it uncached is what keeps that true if
it ever changes: tiles must not already be pooled under a key that ignores a parameter.

`cache seed` and `cache purge` take `--tile-matrix-set`. One run covers one scheme: it enumerates a
single tile pyramid, so a run cannot cover two schemes at once.

```sh
# defaults to the map's own scheme
shigola cache seed --config=config.toml --map=parks --bounds=…

# or name one explicitly
shigola cache seed --config=config.toml --tile-matrix-set=WorldCRS84Quad --bounds=…
```

Without `--map`, a run defaults to WebMercatorQuad. If any targeted map does not support the run's
scheme, the run fails and names those maps rather than skipping them: seeding a map on the wrong
pyramid writes tiles no request will ever ask for, and would otherwise report success.

`--bounds` is lng/lat and `--bounds-srid` describes those bounds; neither selects the tiling scheme.
Note that bounds landing exactly on a tile edge no longer include the tile on the far side.

## Conformance

`/conformance` declares:

```
http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/core
http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/landingPage
http://www.opengis.net/spec/ogcapi-common-2/1.0/conf/collections
http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/json
http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/oas30
http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/core
http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/tileset
http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/tilesets-list
http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/mvt
http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/geodata-tilesets
http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/oas30
```

**Verified against the OGC CITE suite** (`ets-ogcapi-tiles10` 1.2, via TeamEngine), serving the
Athens OSM extract out of the PostGIS fixture through `ST_AsMVT`:

```
15 passed · 0 failed · 1 untested     WebMercatorQuad
15 passed · 0 failed · 1 untested     WorldCRS84Quad
```

The untested assertion is `.../conf/dataset-tilesets`, which this service does not implement and
does not declare — tilesets are per collection, not for the dataset as a whole.

The responses are also validated against the OGC schemas the standard points at, which CITE does
not check exhaustively: the tileset metadata against
[`tms/2.0/json/tileSet.json`](https://schemas.opengis.net/tms/2.0/json/tileSet.json), and the
tilesets list against the schema embedded in Requirement 10 C. Both validate with no errors.

### Running CITE

CI runs this suite on both schemes — see `.github/workflows/ogc_cite.yml`, which drives
`.github/cite/run.sh`. It triggers on changes to the OGC surface, on demand, and weekly, since the
suite is versioned separately from this repository and a passing implementation can start failing
without a commit.

The suite's data source is the Athens OSM extract in the PostGIS fixture, served through the
`mvt_postgis` provider, so a run needs the fixture up first. To reproduce a CI run locally:

```sh
docker compose up -d && docker wait migration       # the Athens fixture, in PostGIS
CGO_ENABLED=0 go build -mod vendor -tags noGpkgProvider -o /tmp/shigola ./cmd/shigola
/tmp/shigola serve --config .github/cite/config.toml --port ":8081" &
.github/cite/run.sh WebMercatorQuad 14 6324 9271
.github/cite/run.sh WorldCRS84Quad 14 4740 18542
```

Each run prints `<scheme>: 15 passed, 1 untested` and then `<scheme>: OK`. `run.sh` enforces a
floor of 15 passed assertions (`MIN_PASSED`), because the EARL report has no summary line and a run
that reached nothing at all reports no failures.

The build flags say something the config cannot. The conformance fixture used to be a GeoPackage,
which made this suite — the project's only external conformance evidence — an invisible dependency
of a provider that is on its way out; building without the GeoPackage provider is what turns
"conformance passes with no GeoPackage present" into a fact about the binary. `CGO_ENABLED=0` is
already enough on its own, since the `init()` that registers the provider is behind
`//go:build cgo` and `config.Validate` then rejects the `gpkg` type outright. `-tags
noGpkgProvider` is belt and braces — and it is the half that keeps holding if someone needs cgo
back for an unrelated reason.

The fixture's layers declare a **narrow zoom window** (13–15), which is deliberate and is about
accuracy rather than about data volume: `ST_AsMVTGeom` maps the bounding box onto the tile grid
affinely, and one SQL string cannot be affine-correct for a mercator grid and a geographic one at
once. See the comments in `.github/cite/config.toml` for the arithmetic. `TestCiteConformanceWorkflowTiles`
fails if the workflow asks for a zoom outside the window, so widening it is a deliberate act.

The tile arguments are `<tileMatrixSetId> <tileMatrix> <tileRow> <tileCol>`, row before column, and
they are not interchangeable between schemes: a WorldCRS84Quad tile is half the width and a bit
over half the height of a WebMercatorQuad tile at the same zoom, so the same ground has a different
index in each. Both tiles above hold all three layers, which matters because `ST_AsMVT` emits
nothing at all for a layer with no rows and the suite reports no failure for a tile that is empty.
`TestCiteConformanceWorkflowTiles` checks these arguments against the config that serves them.

The manual equivalent, for reference:

The suite needs a TeamEngine instance and a running shigola it can reach, serving real data. Both
run as containers on one docker network — and shigola now needs to reach PostGIS as well, so the
fixture joins that network and the config it serves has to name the database by a hostname a
container can resolve, not `localhost`:

```sh
docker network create cite-net

# 0. the fixture, reachable from the network the server is on. `uri` in the
#    config copy under citedata/ has to point at postgis:5432, not localhost.
docker compose up -d && docker wait migration
docker network connect cite-net postgis

# 1. shigola, serving a map with data
docker run -d --name cite-shigola --network cite-net \
  -v "$PWD/citedata:/data" -w /data --entrypoint /data/shigola \
  shigola-dev:latest serve --config /data/config.toml

# 2. TeamEngine with the OGC API - Tiles suite
docker run -d --name cite-te --network cite-net -p 8888:8080 ogccite/ets-ogcapi-tiles10

# 3. run the suite. Credentials are ogctest/ogctest.
curl -u ogctest:ogctest -G \
  --data-urlencode "iut=http://cite-shigola:8080/" \
  --data-urlencode "noofcollections=-1" \
  --data-urlencode "tilematrixsetdefinitionuri=http://www.opengis.net/def/tilematrixset/OGC/1.0/WebMercatorQuad" \
  --data-urlencode "urltemplatefortiles=http://cite-shigola:8080/collections/athens/tiles/WebMercatorQuad/{tileMatrix}/{tileRow}/{tileCol}" \
  --data-urlencode "tilematrix=14" \
  --data-urlencode "mintilerow=6324" --data-urlencode "maxtilerow=6324" \
  --data-urlencode "mintilecol=9271" --data-urlencode "maxtilecol=9271" \
  http://localhost:8888/teamengine/rest/suites/ogcapi-tiles-1.0/run
```

The last six arguments are **test inputs the suite does not discover for itself**. Omit them and
three `MandatoryCore` tests fail with "A tile matrix set definition uri was not found in the test
inputs" — which reads like a defect in the service and is not one. The row and column above are a
tile that actually holds data; pick one inside the collection's `tileMatrixSetLimits`.

The output is an EARL report in RDF/XML. It has no summary line: count `earl:outcome` values.

What the repository's own tests cover, and what CITE adds on top:

- covered by `go test`: every resource's shape and links; every published link resolves, including
  behind a mount prefix; the tile template resolves once filled in; a transposed tile path is
  rejected; tiles differ between schemes; the OpenAPI document describes every registered route.
- added by CITE: the specification's own assertions about response schemas, headers and operation
  ids, against a real data source. It found one thing the local tests could not — the tile
  operation's id must contain `.getTile`, per Requirement /req/oas30/operation-id and Table 11.
