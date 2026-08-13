# OGC API - Tiles

tegola serves [OGC API - Tiles](https://ogcapi.ogc.org/tiles/) for vector (Mapbox Vector Tile)
data, alongside its native `/maps/...` routes.

## Upgrading — two breaking changes

**1. The viewer moved from `/` to `/viewer`.**

OGC API - Tiles requires a landing page at the service root, so the embedded viewer moved.
`/viewer` redirects to `/viewer/`; the viewer's assets are referenced relatively and only resolve
from a URL ending in a slash. Update any bookmark, reverse-proxy rule or health check that pointed
at `/`. A request for `/` now returns the JSON landing page.

An unknown path now returns 404. Previously the viewer's catch-all sat at the service root and
answered everything.

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
tegola cache purge --config=config.toml --bounds=-180,-85.0511,180,85.0511 --max-zoom=…
tegola cache seed  --config=config.toml --bounds=-180,-85.0511,180,85.0511 --max-zoom=…
```

For a file or S3 cache, deleting the old directory tree is faster than purging tile by tile.

## Configuration

`tile_srid` is replaced by `tile_matrix_sets`, which names the tiling schemes a map may be
requested in:

```toml
[[maps]]
  name = "parks"
  # Omit for every scheme this build serves. The first entry is the map's
  # default: the scheme its native /maps/... routes serve.
  tile_matrix_sets = ["WebMercatorQuad", "WorldCRS84Quad"]
```

Schemes are configured per map, not per layer. A map's layer-collections offer exactly the schemes
their map does.

This build serves the schemes that need no coordinate transformation backend:

| tileMatrixSetId | CRS | Matrix at zoom z |
|---|---|---|
| `WebMercatorQuad` | EPSG:3857 | 2^z × 2^z |
| `WorldCRS84Quad` | OGC:CRS84 | 2·2^z × 2^z |
| `WGS1984Quad` | EPSG:4326 | 2^z × 2^z |

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
transposed from tegola's native `/maps/{map}/{z}/{x}/{y}`, which is zoom, column, row.

```
/maps/parks/3/5/2                                  z=3 x=5 y=2
/collections/parks/tiles/WebMercatorQuad/3/2/5      z=3 y=2 x=5   — the same tile
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

`mvt` is canonical: it is what every link and template this service emits says, and it is the name
in the OGC conformance class. `pbf` is accepted because that is what the same tile is called by
tegola's native routes, which serve it at a `.pbf` extension, and by the `format` member of the
TileJSON above — being refused for using our own word for it would be surprising. Matching ignores
case. The alias resolves to MVT before a resource's own formats are consulted, so `?f=pbf` on a
JSON-only resource is still a 400.

## Caching

OGC tile requests use the same cache keys as the native routes, so a tile seeded through
`tegola cache seed` is served by both, and neither generates it twice.

`cache seed` and `cache purge` take `--tile-matrix-set`. One run covers one scheme: it enumerates a
single tile pyramid, so a run cannot cover two schemes at once.

```sh
# defaults to the map's own scheme
tegola cache seed --config=config.toml --map=parks --bounds=…

# or name one explicitly
tegola cache seed --config=config.toml --tile-matrix-set=WorldCRS84Quad --bounds=…
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
Athens OSM GeoPackage from `provider/gpkg/testdata`:

```
15 passed · 0 failed · 1 skipped     WebMercatorQuad
15 passed · 0 failed · 1 skipped     WorldCRS84Quad
```

The skip is `.../conf/dataset-tilesets`, which this service does not implement and does not
declare — tilesets are per collection, not for the dataset as a whole.

The responses are also validated against the OGC schemas the standard points at, which CITE does
not check exhaustively: the tileset metadata against
[`tms/2.0/json/tileSet.json`](https://schemas.opengis.net/tms/2.0/json/tileSet.json), and the
tilesets list against the schema embedded in Requirement 10 C. Both validate with no errors.

### Running CITE

The suite needs a TeamEngine instance and a running tegola it can reach, serving real data. Both
run as containers on one docker network:

```sh
docker network create cite-net

# 1. tegola, serving a map with data
docker run -d --name cite-tegola --network cite-net \
  -v "$PWD/citedata:/data" -w /data --entrypoint /data/tegola \
  tegola-dev:latest serve --config /data/config.toml

# 2. TeamEngine with the OGC API - Tiles suite
docker run -d --name cite-te --network cite-net -p 8888:8080 ogccite/ets-ogcapi-tiles10

# 3. run the suite. Credentials are ogctest/ogctest.
curl -u ogctest:ogctest -G \
  --data-urlencode "iut=http://cite-tegola:8080/" \
  --data-urlencode "noofcollections=-1" \
  --data-urlencode "tilematrixsetdefinitionuri=http://www.opengis.net/def/tilematrixset/OGC/1.0/WebMercatorQuad" \
  --data-urlencode "urltemplatefortiles=http://cite-tegola:8080/collections/athens/tiles/WebMercatorQuad/{tileMatrix}/{tileRow}/{tileCol}" \
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
