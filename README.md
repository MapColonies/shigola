# Shigola

[![Report Card](https://goreportcard.com/badge/github.com/MapColonies/shigola)](https://goreportcard.com/badge/github.com/MapColonies/shigola)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/MapColonies/shigola)
[![license](http://img.shields.io/badge/license-MIT-red.svg?style=flat)](LICENSE.md)

Shigola is a vector tile server delivering [Mapbox Vector Tiles](https://github.com/mapbox/vector-tile-spec) with support for [PostGIS](https://postgis.net/), [GeoPackage](https://www.geopackage.org/) and [SAP HANA Spatial](https://www.sap.com/products/technology-platform/hana/what-is-sap-hana.html) data providers.

> ### Shigola is a fork of Tegola
>
> [Tegola](https://github.com/go-spatial/tegola) is an open source vector tile server created and
> maintained by the **[Go Spatial](https://github.com/go-spatial) team**, MIT licensed, and
> documented at [tegola.io](https://tegola.io). Shigola is a fork of it maintained by
> [MapColonies](https://github.com/MapColonies), and everything good about the software described
> here originates upstream.
>
> Shigola adds three things Tegola does not have — [OGC API - Tiles](docs/ogc-api-tiles.md),
> multiple [tile matrix sets](docs/ogc-api-tiles.md#configuration), and a
> [layered cache](#layered-cache) — and is otherwise additive. For anything not listed as a Shigola
> feature, upstream behaviour and the [official Tegola docs](https://tegola.io) apply.
>
> **Bug reports about behaviour this fork did not change belong
> [upstream](https://github.com/go-spatial/tegola/issues).** See [Relationship to
> Tegola](#relationship-to-tegola) for what changed, including the breaking changes.

## Features

- Native geometry processing (simplification, clipping, make valid, intersection, contains, scaling, translation)
- [Mapbox Vector Tile v2 specification](https://github.com/mapbox/vector-tile-spec) compliant.
- Support for [PostGIS](provider/postgis) and [GeoPackage](provider/gpkg) data providers. Extensible design to support additional data providers.
- Support for several cache backends: [file](cache/file), [s3](cache/s3), [redis](cache/redis), [azure blob store](cache/azblob).
- [Layered caching](#layered-cache): an ordered chain of cache backends with read-through promotion, per-tier read deadlines and non-blocking writes.
- Cache seeding and invalidation via individual tiles (ZXY), lat / lon bounds and ZXY tile list.
- Parallelized tile serving and geometry processing.
- Support for Web Mercator (3857) and WGS84 (4326) projections.
- Support for [AWS Lambda](cmd/shigola_lambda).
- Support for serving HTTPS.
- Support for [PostGIS ST_AsMVT](mvtprovider/postgis).
- Support for [Prometheus](observability/prometheus/README.md) observability.

## Usage

```
shigola is a vector tile server
Version: v0.21.0

Usage:
  shigola [command]

Available Commands:
  cache       Manipulate the tile cache
  help        Help about any command
  serve       Use shigola as a tile server
  version     Print the version number of shigola

Flags:
      --config string   path to config file (default "config.toml")
  -h, --help            help for shigola

Use "shigola [command] --help" for more information about a command.
```

## Running shigola as a vector tile server

1. Download the appropriate binary of shigola for your platform via the [release page](https://github.com/MapColonies/shigola/releases).
2. Set up your config file and run. By default, Shigola looks for a `config.toml` in the same directory as the binary. You can set a different location for the `config.toml` using a command flag:

```
./shigola serve --config=/path/to/config.toml
```

## Server Endpoints

```
/
```

The server root returns the OGC API - Tiles landing page.

```
/maps/:map_name/:z/:x/:y
```

Return vector tiles for a map. The URI supports the following variables:

- `:map_name` is the name of the map as defined in the `config.toml` file.
- `:z` is the zoom level of the map.
- `:x` is the row of the tile at the zoom level.
- `:y` is the column of the tile at the zoom level.

```
/maps/:map_name/:layer_name/:z/:x/:y
```

Return vector tiles for a map layer. The URI supports the same variables as the map URI with the additional variable:

- `:layer_name` is the name of the map layer as defined in the `config.toml` file.

```
/maps/:map_name/style.json
```

Return an auto generated [Mapbox GL Style](https://www.mapbox.com/mapbox-gl-js/style-spec/) for the configured map.

## Configuration

The shigola config file uses the [TOML](https://github.com/toml-lang/toml) format. The following example shows how to configure a `mvt_postgis` data provider. The `mvt_postgis` provider will leverage PostGIS's `ST_AsMVT()` function for the encoding of the vector tile.

Under the `maps` section, map layers are associated with data provider layers and their `min_zoom` and `max_zoom` values are defined.

### Example config using Postgres 12+ / PostGIS 3.0 ST_AsMVT():

```toml
# register a MVT data provider. MVT data providers have the prefix "mvt_" in their type
# note mvt data providers can not be conflated with any other providers of any type in a map
# thus a map may only contain a single mvt provider.
[[providers]]
name = "my_postgis"         # provider name is referenced from map layers (required).
type = "mvt_postgis"        # the type of data provider must be "mvt_postgis" for this data provider (required)
uri = "postgresql://shigola:<password>@localhost:5432/shigola?ssl_mode=prefer" # database connection string

  [[providers.layers]]
  name = "landuse"
  # MVT data provider must use SQL statements
  # this table uses "geom" for the geometry_fieldname and "gid" for the id_fieldname so they don't need to be configured
  # Wrapping the geom with ST_AsMVTGeom is required.
  sql = "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, gid FROM gis.landuse WHERE geom && !BBOX!"
  # If you want to use the configurable parameters defined in maps.params make sure to include the token in the SQL statement
  sql = "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, gid FROM gis.landuse WHERE geom && !BBOX! !PARAM!"

# maps are made up of layers
[[maps]]
name = "zoning"                           # used in the URL to reference this map (/maps/zoning)

  [[maps.layers]]
  name = "landuse"                        # name is optional. If it's not defined the name of the ProviderLayer will be used.
  provider_layer = "my_postgis.landuse"   # must match a data provider layer
  min_zoom = 10                           # minimum zoom level to include this layer
  max_zoom = 16                           # maximum zoom level to include this layer

  # configure addition URL parameters: /maps/:map_name/:layer_name/:z/:x/:y?param=value
  # which will be passed to the database queries
  [[maps.params]]
  name          = "param"         # name used in the URL
  token         = "!PARAM!"       # token to replace in providers.layers.sql query
  type          = "string"        # one of: int, float, string, bool
  sql           = "AND param = ?" # SQL to replace the token in the query. ? will be replaced with a parameter value. If omitted, defaults to "?"
  # if neither default_value nor default_sql is specified, the URL parameter is required to be present in all queries
  # either
  default_value = "value"         # if parameter is not specified, this value will be passed to .sql parameter
  # or
  default_sql   = " "             # if parameter is not specified, this value will replace the .sql parameter. Useful for omitting query entirely
```

- More information on PostgreSQL SSL modes can be found [here](https://www.postgresql.org/docs/current/libpq-ssl.html).
- More information on the `mvt_postgis` provider can be found [here](mvtprovider/postgis)

## Layered cache

`type = "multi"` puts an ordered chain of cache backends behind the single
`[cache]` table. Reads walk the tiers in declaration order and a hit in a later
tier is promoted into the earlier ones; writes fan out to every tier; purges run
in reverse.

The motivating deployment is Redis in front of S3 — a small, fast, evicting tier
over a large, durable one — but the mechanism is generic. Any registered cache
type can occupy any position, in any number, and a tier may itself be a chain.

```toml
[cache]
type           = "multi"
promote_on_hit = true          # default; false gives a read-only fan-out

  [[cache.layers]]
  type       = "redis"
  ttl        = 3600            # seconds — an existing redis key
  timeout_ms = 35              # abandon this tier's read after 35ms
  name       = "hot"           # optional; pins the metric label and the
                               # --cache-tiers value against reordering

  [[cache.layers]]
  type   = "s3"
  bucket = "tiles"
  # timeout_ms omitted: the durable tier is allowed to be slow.
```

**Declaration order is read order.** There is no `priority` key — a TOML list is
already ordered, and a second ordering mechanism would be a second source of
truth.

Note that `[[cache.layers]]` headers are *siblings* however deeply they are
indented; TOML indentation is cosmetic. Real nesting needs the key to nest:
`[[cache.layers.layers]]`.

### `timeout_ms`

An optional per-cache read deadline, in **integer milliseconds**. It carries its
unit where the adjacent `ttl` takes bare **seconds** — deliberately, because a
bare `timeout` next to `ttl = 3600` reads as seconds.

It is not a chain key: it applies to any cache at any nesting depth, including a
plain non-chained `[cache]` table, and on the chain itself it acts as a
whole-chain read budget. `Get` only.

| Backend | `timeout_ms` on `Get` | |
|---|---|---|
| `redis`, `s3`, `azblob`, `gcs` | **enforced** | all four attach the context to the request |
| `file` | **advisory** | `os.Open`/`Stat` block before its one `ctx.Err()` check. This matters on an NFS/EFS mount, where a `file` tier really is a network cache — use mount-level `soft` and `timeo=` instead. Not fixable: Go cannot cancel a blocking filesystem syscall. |
| `memory` | n/a | a map; it returns before any deadline could fire |

Deadlines compose additively down the chain: `redis 35ms` plus `s3 2000ms` is a
2.035s worst case before the chain concludes "miss" and generation even begins.
A `timeout_ms` on the top-level `[cache]` caps that, and is not the default.

**A read failure is a miss, never an error.** A tier that fails is logged,
counted and skipped, and a chain in which every tier failed still returns a
miss. That is what keeps tiles being written back to a healthy tier while
another one is down — but it also means a broken tier produces no error, no
status-code change and no latency change. The per-tier error counter is the only
evidence. See [Operating a layered cache](#operating-a-layered-cache).

### Writes do not block the response

**Every** cache — chain or not — now hands its writes to a bounded pool and
returns; the response is flushed first. A write that cannot claim a slot is
dropped and counted rather than queued.

This is a behaviour change for single-backend deployments too, not just for
`type = "multi"`.

Dropping is safe: a discarded write only means the tile is regenerated or
re-read from the durable tier later. Dropping *silently* is not, which is why
the counters below exist.

The CLI seed/purge worker and the Lambda entrypoint write inline instead. Both
would otherwise lose writes at process exit or execution freeze.

### Operational switches

These three live in `SHIGOLA_OPTIONS` rather than in `[cache]`, because they are
process resourcing and lifecycle rather than cache configuration — and each has
to be changeable during the incident that reveals the need for it.

```
SHIGOLA_OPTIONS=DetachedWriteSlots=1024        # pool capacity;    default 256
SHIGOLA_OPTIONS=DetachedWriteTimeoutMs=10000   # bound on writes;  default 10000, 0 disables
SHIGOLA_OPTIONS=DetachedWriteDrainMs=5000      # shutdown drain;   default 5000,  0 disables
```

`DetachedWriteSlots` is the one knob that can exhaust process memory: worst-case
live write buffers are `slots × tiers × average tile size`, so the 256 default is
roughly 100 MB with two tiers and 200 KB tiles, and 1024 is roughly 400 MB.

`DetachedWriteTimeoutMs` bounds **slot occupancy**, not a request — the user's
request finished long before. It is on by default because nothing else bounds an
S3 write, and a wedged write holds its slot forever: enough of them over a
process lifetime empty the pool, after which every write is dropped until the
process restarts.

Values are integers, and the parser does not accept duration strings — write
`DetachedWriteTimeoutMs=10000`, not `10s`.

### Seeding a layered cache

```
shigola cache seed --map=osm                          # writes the LAST tier only
shigola cache seed --map=osm --cache-tiers=all        # pre-warm: write every tier
shigola cache seed --map=osm --cache-tiers=hot,s3     # an explicit list
shigola cache seed --map=osm --overwrite              # write, then purge the rest
```

**`seed` writes only the last tier by default.** Seeding every tier would flood
the hot tier with cold tiles in seed order, evicting the live working set — the
exact harm the chain exists to avoid. The last tier in read order is the durable
one by construction, so no heuristic and no extra config key is needed.

Two consequences worth knowing:

- **Adding a tier to an existing chain changes what `seed` writes.** A
  single-backend cache is unaffected: one cache is also the last cache.
- **This assumes tiers are ordered hot → durable, and nothing enforces it.** A
  chain of `s3` then `redis` is legal, and makes `seed` write the hot tier and
  skip the durable one.

`--cache-tiers` takes tier names, validated at startup; an unknown name is an
error rather than a silent no-op. A nested tier is addressed by path
(`nested/inner`). When set, it bounds promotion as well as writes, so
`--cache-tiers=s3` cannot reach the hot tier by either route.

**`--overwrite` purges the tiers it does not write, after writing them.** Without
that, a re-seed with the durable-only default would leave the hot tier serving
pre-update tiles until TTL expiry — so the command documented as the invalidation
mechanism would not invalidate what users are served. Ordering matters: writing
first closes the window in which a concurrent read could promote the old tile
back.

Invalidation is re-seeding. There is no TTL refresh on read, so in a Redis→S3
chain the Redis TTL bounds Redis memory rather than staleness: an expired tile is
re-read from S3 and promoted, unchanged. That churn — one durable-tier GET and
one promotion per hot tile per TTL period — is the cost of a short TTL, and a
rising durable-tier hit rate is the signal it is set too short.

### Metrics

With an observer configured there are **two families, and they must not be
summed together**.

| Family | Scope | `tier` label |
|---|---|---|
| `shigola_cache_*` | the whole cache — one hit means "served from somewhere" | no |
| `shigola_cache_tier_*` | one tier — several lookups per request | yes |

`sum(shigola_cache_tier_hits_total)` is **not** the chain hit count; use
`shigola_cache_hits_total`.

The pool and the chain publish their own counters:

| Metric | Means |
|---|---|
| `shigola_cache_write_slots_in_flight` / `_capacity` | pool saturation — the leading indicator |
| `shigola_cache_writes_dropped_total` | the pool was full at admission; nothing was attempted |
| `shigola_cache_writes_abandoned_total` | still running when the shutdown drain expired |
| `shigola_cache_writes_timed_out_total` | killed by `DetachedWriteTimeoutMs`; also counted in `_writes_failed_total` |
| `shigola_cache_writes_failed_total`, `_writes_completed_total`, `_write_duration_seconds_total` | attempted writes |
| `shigola_cache_promotions_total`, `_promotions_dropped_total` | read-through promotion |
| `shigola_cache_tier_read_timeouts_total` | reads abandoned by their `timeout_ms`; also counted in `_errors_total` |

> **Renamed metric.** The cache error counter was registered as the unprefixed
> `errors`. It is now `shigola_cache_errors_total` (and
> `shigola_cache_tier_errors_total` per tier). Dashboards and alerts referring to
> `errors` need updating.

### Operating a layered cache

This design degrades silently by construction, so these are part of the feature
rather than decoration.

**The write path escalates in a fixed order — alert in that order.**

| # | Signal | Means | Do |
|---|---|---|---|
| 1 | `rate(shigola_cache_writes_timed_out_total) > 0` | writes are hitting the bound; the durable tier is degrading | investigate now — this is the earliest warning |
| 2 | `in_flight / capacity > 0.7` for 5m | the pool is filling | raise `DetachedWriteSlots`, or fix what is slowing writes |
| 3 | `rate(shigola_cache_writes_dropped_total) > 0` | the pool is exhausted and writes are being lost | both of the above, urgently |

**The read path has one alert that is not optional.**
`rate(shigola_cache_tier_errors_total) > 0` should page, at least for the durable
tier. Read failures degrade to a miss, so a broken tier produces no error, no
status-code change and no latency change — tiles keep serving, regenerated from
the database. This counter is the only evidence.

Do not alert on whole-cache `Set` latency: it measures slot acquisition, which is
near zero and always succeeds. And do not alert on chain hit rate alone — a
healthy cache with a genuinely cold working set looks identical to a broken one.
Pair it with the tier error rate.

**One risk this does not address.** A total cache outage means every request
regenerates its tile from the database. Nothing at the cache seam can prevent
that — single-flight, circuit breaking and stale-while-revalidate all need work
above the cache — and it is no worse than a single-backend cache outage today.

## Environment Variables

#### Config TOML

Environment variables can be injected into the configuration file. One caveat is that the injection has to be within a string, though the value it represents does not have to be a string.

The above config example could be written as:

```toml
# register data providers
[[providers]]
name = "test_postgis"
type = "mvt_postgis"
uri = "${POSTGIS_CONN_STR}"  # database connection string
srid = 3857
max_connections = "${POSTGIS_MAX_CONN}"
```

## SQL Debugging

The following environment variables can be used for debugging:

`SHIGOLA_SQL_DEBUG` specify the type of SQL debug information to output. Currently, supporting two values:

- `LAYER_SQL` will print layer SQL as they are parsed from the config file.
- `EXECUTE_SQL` will print SQL that is executed for each tile request, and the number of items it returns or an error.

#### Usage

```bash
$ SHIGOLA_SQL_DEBUG=LAYER_SQL shigola serve --config=/path/to/conf.toml
```

The following environment variables can be used to control various runtime options on dataproviders that are **NOT** `mvt_postgis`:

`SHIGOLA_OPTIONS` specify a set of options comma or space delimited. Supports the following options

- `DontSimplifyGeo` to turn off simplification for all layers.
- `SimplifyMaxZoom={{int}}` to set the max zoom that simplification will apply to. (14 is default)

## Client side debugging

When debugging client side, it's often helpful to see an outline of a tile along with it's Z/X/Y values. To encode a debug layer into every tile add the query string variable `debug=true` to the URL template being used to request tiles. For example:

```
http://localhost:8080/maps/mymap/{z}/{x}/{y}.vector.pbf?debug=true
```

The requested tile will be encoded with a layer that has the `name` value set to `debug` and includes the three following features.

- `debug_outline` is a line feature that traces the border of the tile
- `debug_text` is a point feature in the middle of the tile with the following tags:
- `zxy` is a string with the `Z`, `X` and `Y` values formatted as: `Z:0, X:0, Y:0`

## Building from source

Shigola is written in [Go](https://golang.org/) and requires [Go 1.26.2](https://go.dev/dl/) or higher to compile from the source.
(We support the two newest versions of Go.)
To build shigola from the source, make sure you have Go installed and have cloned the repository.
Navigate to the repository then run the following command:

```bash
go generate ... && cd cmd/shigola/ && go build -mod vendor
```

You will now have a binary named `shigola` in the current directory which is [ready to run](#running-shigola-as-a-vector-tile-server).

**Build Flags**
The following build flags can be used to turn off certain features of shigola:

- `noAzblobCache` - turn off the Azure Blob cache back end.
- `noS3Cache` - turn off the AWS S3 cache back end.
- `noRedisCache` - turn off the Redis cache back end.
- `noGCSCache` - turn off the Google Cloud Storage cache back end.
- `noPostgisProvider` - turn off the PostGIS data provider.
- `noGpkgProvider` - turn off the GeoPackage data provider. Note, GeoPackage uses CGO and will be turned off if the environment variable `CGO_ENABLED=0` is set prior to building.
- `noHanaProvider` - turn off the SAP HANA data provider.
- `pprof` - enable [Go profiler](https://golang.org/pkg/net/http/pprof/). Start profile server by setting the environment `SHIGOLA_HTTP_PPROF_BIND` environment (e.g. `SHIGOLA_HTTP_PPROF_BIND=localhost:6060`).
- `noPrometheusObserver` - turn off support for the Prometheus metric end point.

Example of using the build flags to turn off the Redis cache back end and the GeoPackage provider.

```bash
go build -tags 'noRedisCache noGpkgProvider'
```

**Setting Version Information** The following flags can be used to set version information:

```bash
# first set some env to make it easier to read:
BUILD_PKG=github.com/MapColonies/shigola/internal/build
VERSION=1.16.x
GIT_BRANCH=$(git branch --no-color --show-current)
GIT_REVISION=$(git log HEAD --oneline | head -n 1 | cut -d ' ' -f 1)

# build the go binary
go build -ldflags "-w -X ${BUILD_PKG}.Version=${VERSION} -X ${BUILD_PKG}.GitRevision=${GIT_REVISION} -X ${BUILD_PKG}.GitBranch=${GIT_BRANCH}"
```

## Relationship to Tegola

Shigola is a fork of [go-spatial/tegola](https://github.com/go-spatial/tegola). It is additive
except where noted below.

### What Shigola adds

- **[OGC API - Tiles](docs/ogc-api-tiles.md)** — a standards-compliant tile API alongside the native
  `/maps/...` routes, verified against the OGC CITE executable test suite.
- **Multiple tile matrix sets** — `WebMercatorQuad`, `WorldCRS84Quad` and `WGS1984Quad`, selectable
  per map with `tile_matrix_sets`, where Tegola serves one implicit scheme.
- **[Layered cache](#layered-cache)** — `type = "multi"`: an ordered chain of cache backends with
  read-through promotion, per-tier read deadlines and non-blocking writes.

### Breaking changes vs Tegola

| Change | Consequence |
|:---|:---|
| The service root is the OGC API - Tiles landing page | `/` returns JSON. An unknown path returns 404. |
| Cache keys gained a leading `{tileMatrixSetId}` | Existing cache entries are unreachable. **Purge and re-seed.** |
| The binary is `shigola`, not `tegola` | Update deploy scripts, Dockerfiles and unit files. |
| Prometheus metrics are `shigola_*`, not `tegola_*` | **Dashboards and alerts need updating** — `tegola_cache_hits_total` is now `shigola_cache_hits_total`, and so on. Nothing emits the old names. |

Environment variables are now `SHIGOLA_*`. The `TEGOLA_*` names still work and log a deprecation
warning, so an existing deployment keeps running — an unset variable is not an error, and dropping
the old names outright would have changed behaviour silently.

### Attribution

- **Tegola** — © the [Go Spatial](https://github.com/go-spatial) team, MIT licensed. Shigola keeps
  that license and is a derivative work. Upstream's copyright notice is preserved in
  [LICENSE.md](LICENSE.md) and its contributors in [CONTRIBUTORS.md](CONTRIBUTORS.md).
- **morecantile** — the [`tms`](tms/) package is a faithful Go port of
  [developmentseed/morecantile](https://github.com/developmentseed/morecantile) 7.0.3, MIT licensed
  © Development Seed. Its license is reproduced at [tms/LICENSE-morecantile](tms/LICENSE-morecantile).

## License

MIT — see [LICENSE.md](LICENSE.md), which retains the original copyright notice of the project this
is derived from.

Third-party code redistributed here, and the works this software is derived from, are listed in
[NOTICE.md](NOTICE.md).

## Looking for a vector tile style editor?

After Shigola is running you're likely going to want to work on your map's cartography.
Give [fresco](https://github.com/go-spatial/fresco) a try!
