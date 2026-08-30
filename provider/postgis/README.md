# PostGIS

The PostGIS provider manages querying for tile requests against a Postgres
database with the [PostGIS](http://postgis.net/) extension installed.

There is one PostGIS provider type, `mvt_postgis`: the tile is encoded by the
database with [`ST_AsMVT`](https://postgis.net/docs/ST_AsMVT.html), and shigola
serves the bytes it returns. A `postgis` type existed alongside it until
MAPCO-11490 and did the encoding in Go from raw geometry; a config still naming
it is rejected at startup with a message naming `mvt_postgis`.

The connection between shigola and Postgis is configured in a `shigola.toml` file.
An example minimum connection config:

```toml
[[providers]]
# provider name is referenced from map layers (required)
name = "test_postgis"

# the type of data provider must be "mvt_postgis" for this data provider (required)
type = "mvt_postgis"

# PostGIS connection string (required)
uri = "postgres://shigola:supersecret@localhost:5432/shigola?sslmode=prefer" #

# PostGIS connection config run time parameter to label
# the origin of a connection
# The default is "shigola"
# (optional)
application_name = "shigola"

# PostGIS connection config run time parameter (optional)
# A read-only SQL transaction cannot alter non-temporary tables.
# This parameter controls the default read-only status of each new transaction.
# The default is OFF (read/write).
# (optional)
default_transaction_read_only = "off"
```

## Connection Properties

Establishing a connection via connection string (`uri`) will become the default
connection method as of v0.16.0. Connecting via host/port/database is deprecated.

-   `uri` (string): [Required] PostGIS connection string
-   `name` (string): [Required] provider name is referenced from map layers
-   `type` (string): [Required] the type of data provider. must be "mvt_postgis" to use this data provider
-   `srid` (int): [Optional] The default SRID for the provider. Defaults to WebMercator (3857) but also supports WGS84 (4326)

### Connection string properties

#### Example

```
# {protocol}://{user}:{password}@{host}:{port}/{database}?{options}=
postgres://shigola:supersecret@localhost:5432/shigola?sslmode=prefer&pool_max_conns=10
```

#### Options

Shigola uses [pgx](https://github.com/jackc/pgx/blob/master/pgxpool/pool.go#L111) to manage
PostgresSQL connections that allows the following configurations to be passed
as parameters.

-   `sslmode`: [Optional] PostGIS SSL mode. Default: "prefer"
-   `pool_min_conns`: [Optional] The min connections to maintain in the connection pool. Defaults to 100. 0 means no max.
-   `pool_max_conns`: [Optional] The max connections to maintain in the connection pool. Defaults to 100. 0 means no max.
-   `pool_max_conn_idle_time`: [Optional] The maximum time an idle connection is kept alive. Defaults to "30m".
-   `pool_max_connection_lifetime` [Optional] The maximum time a connection lives before it is terminated and recreated. Defaults to "1h".
-   `pool_max_conn_lifetime_jitter` [Optional] Duration after `max_conn_lifetime` to randomly decide to close a connection.
-   `pool_health_check_period` [Optional] Is the duration between checks of the health of idle connections. Defaults to 1m

## Provider Layers

In addition to the connection configuration above, Provider Layers need to be
configured. A Provider Layer tells shigola how to query PostGIS for a certain
layer. The geometry MUST be wrapped in
[`ST_AsMVTGeom()`](https://postgis.net/docs/ST_AsMVTGeom.html): shigola wraps
each layer's SQL in `ST_AsMVT()` and serves the bytes the database returns, so
the transform into tile coordinates has to happen in the query.

```toml
[[providers.layers]]
name = "landuse"
# this table uses "geom" for the geometry_fieldname and "gid" for the
# id_fieldname so they don't need to be configured
geometry_type = "multipolygon"
sql = "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, gid FROM gis.landuse WHERE geom && !BBOX!"
```

### Provider Layers Properties

-   `name` (string): [Required] the name of the layer. This is used to reference this layer from map layers.
-   `geometry_fieldname` (string): [Optional] the name of the filed which contains the geometry for the feature. defaults to `geom`.
-   `id_fieldname` (string): [Optional] the name of the feature id field. defaults to `gid`.
-   `srid` (int): [Optional] the SRID of the layer. Supports `3857` (WebMercator) or `4326` (WGS84).
-   `geometry_type` (string): [Required in practice] the layer geometry type. Valid values are: `Point`, `LineString`, `Polygon`, `MultiPoint`, `MultiLineString`, `MultiPolygon`, `GeometryCollection`. If it is not set, the layer's SQL is run at startup to infer the type — and a query ending in `ST_AsMVTGeom` returns tile-space geometry that cannot be typed, so the provider fails to start with `returned unsupported geometry type`. Declare it.
-   `sql` (string): [Required] custom SQL to use use. Supports the following tokens:
    -   `!BBOX!` - [Required] will be replaced with the bounding box of the tile before the query is sent to the database. `!bbox!` and`!BOX!` are supported as well for compatibilitiy with queries from Mapnik and MapServer styles.
    -   `!ZOOM!` - [Optional] will be replaced with the "Z" (zoom) value of the requested tile.
    -   `!X!` - [Optional] will be replaced with the "X" value of the requested tile.
    -   `!Y!` - [Optional] will be replaced with the "Y" value of the requested tile.
    -   `!Z!` - [Optional] will be replaced with the "Z" value of the requested tile.
    -   `!SCALE_DENOMINATOR!` - [Optional] scale denominator, assuming 90.7 DPI (i.e. 0.28mm pixel size)
    -   `!PIXEL_WIDTH!` - [Optional] the pixel width in meters, assuming 256x256 tiles
    -   `!PIXEL_HEIGHT!` - [Optional] the pixel height in meters, assuming 256x256 tiles
    -   `!ID_FIELD!` - [Optional] the id field name
    -   `!GEOM_FIELD!` - [Optional] the geom field name
    -   `!GEOM_TYPE!` - [Optional] the geom type field name

A `tablename` may be given instead of `sql`, along with `fields` to choose the
columns it selects. Both are leftovers of the removed standard type, which is
what that path was shaped for, and neither is usable here: shigola generates a
whole-table select with no `ST_AsMVTGeom` and no bounding-box filter, which
`ST_AsMVT` cannot make a correct tile out of.

Which way it fails depends on something unrelated. Without `geometry_type` the
provider refuses to start, because inferring the type means reading that
generated query back and it returns raw geometry. **With `geometry_type` — which
you are told above to always declare — it starts, and serves whole-table tiles.**
Write the `sql`.

#### Example mvt_postgis and map config

```toml
[[providers]]
name = "test_postgis"
type = "mvt_postgis"
uri = "postgres://postgres:postgres@localhost:5432/shigola"

  [[providers.layers]]
  name = "landuse"
  geometry_type = "multipolygon"
  sql = "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, gid FROM gis.landuse WHERE geom && !BBOX!"

[[maps]]
name = "cities"
center = [-90.2,38.6,3.0]  # where to center of the map (lon, lat, zoom)

  [[maps.layers]]
  name = "landuse"
  provider_layer = "test_postgis.landuse"
  min_zoom = 0
  max_zoom = 14
```

#### Example mvt_postgis and map config for SRID 4326

When using a 4326 projection with `ST_AsMVT` the SQL statement needs to be
modified. `ST_AsMVTGeom` is expecting data in 3857 projection so the geometries
and the `!BBOX!` token need to be transformed prior to `ST_AsMVTGeom` processing
them. For example:

```toml
[[providers]]
name = "test_postgis"
type = "mvt_postgis"
uri = "postgres://postgres:postgres@localhost:5432/shigola"
srid = 4326 # setting the srid on the provider to 4326 will cause the !BBOX! value to use the 4326 projection.

  [[providers.layers]]
  name = "landuse"
  # the !BBOX! token used in the WHERE clause is not reprojected so it can match 4326 data
  # the matched data AND the !BBOX! are then reporojected to 3857 prior to being passed to ST_AsMVTGeom
  geometry_type = "multipolygon"
  sql = "SELECT ST_AsMVTGeom(ST_Transform(geom, 3857),ST_Transform(!BBOX!,3857)) AS geom, gid FROM gis.landuse WHERE geom && !BBOX!"

[[maps]]
name = "cities"
center = [-90.2,38.6,3.0]  # where to center of the map (lon, lat, zoom)

  [[maps.layers]]
  name = "landuse"
  provider_layer = "test_postgis.landuse"
  min_zoom = 0
  max_zoom = 14
```

## Environment Variable support

Helpful debugging environment variables:

-   `SHIGOLA_SQL_DEBUG`: specify the type of SQL debug information to output. Supports the following values:
    -   `LAYER_SQL`: print layer SQL as they’re parsed from the config file.
    -   `EXECUTE_SQL`: print SQL that is executed for each tile request and the number of items it returns or an error.
    -   `LAYER_SQL:EXECUTE_SQL`: print `LAYER_SQL` and `EXECUTE_SQL`.

Example:

```bash
$ SHIGOLA_SQL_DEBUG=LAYER_SQL shigola serve --config=/path/to/conf.toml
```

## Testing

Testing is designed to work against a live PostGIS database. `docker compose up -d`
from the repository root brings one up and loads the fixture; the
[CI workflow](../../.github/workflows/on_pr_push.yml) runs the same thing.
To run the PostGIS tests, the following environment variables need to be set:

```bash
$ export RUN_POSTGIS_TESTS=yes
$ export PGURI="postgres://postgres:postgres@localhost:5432/shigola"
$ export PGURI_NO_ACCESS="postgres://shigola_no_access:postgres@localhost:5432/shigola" # used for testing errors when user does not have read permissions on a table
$ export PGPASSWORD=""
$ export PGSSLMODE="disable"
```

`localhost` is right when the compose stack's published port is what you are
dialling. **Inside the devcontainer it is not** — the database is a sibling
service reachable as `postgis`, and `.devcontainer/docker-compose.yml` already
exports `PGURI` and `PGURI_NO_ACCESS` pointing there. Exporting the block above
inside the devcontainer replaces working values with `localhost` and the tests
stop connecting; set only `RUN_POSTGIS_TESTS=yes` there.

### The fixture database

The compose stack's `migration` service restores `testdata/postgis/shigola.dump`
into a database called **`shigola`** and creates the `shigola_no_access` role the
permission-error tests log in as. It also drops the pre-rename `tegola` database
and role, so a volume that predates the rename does not keep a stale copy around
for `PGURI` to find.

The fixture holds two groups of tables:

| Tables | Where they come from |
|:---|:---|
| `hstore_test`, `ne_10m_land_scale_rank`, `null_geom_test`, `osm_buildings_test`, `three_d_test`, and the `as_numeric`/`tilebbox` functions | Inherited from upstream Tegola. No higher-level source exists, so they are carried forward from the previous dump on each regeneration. |
| `land_polygons`, `roads_lines`, `places_points` | Converted from the Athens OSM extract at `testdata/postgis/athens-osm-20170921.gpkg` — the three layers `.github/cite/config.toml` serves through `mvt_postgis`. This is now the OGC conformance suite's only data source, so a fixture that fails to restore takes the conformance run with it. |

`test_tags_table` and `test_warning_log()` are not in the dump; they are applied
afterwards from the `testdata/postgis/postgis-*.sql` files.

### Regenerating the dump

```bash
testdata/postgis/generate-dump.sh
```

Needs Docker and nothing else — it starts its own throwaway PostGIS and GDAL
containers. Run it after changing which Athens layers the fixture carries, or
after adding a table that belongs in the dump rather than in a `postgis-*.sql`
file.

Two things about the dump are worth knowing before editing it:

- **The database name is inside the archive.** A pg_dump custom-format archive
  records the database it was dumped from, and `pg_restore -C` recreates *that*
  name. Renaming every reference in the tree does not rename the database; only
  rebuilding the archive from a correctly named database does.
- **Regeneration is reproducible in content, not in bytes.** pg_dump stamps a
  creation time into the header, so two runs always differ. Review the dump by
  what `pg_restore -l` lists, not by its checksum.

If you're testing SSL, the following additional env vars can be set:

```bash
$ export PGSSLMODE="" // disable, allow, prefer, require, verify-ca, verify-full
$ export PGSSLKEY=""
$ export PGSSLCERT=""
$ export PGSSLROOTCERT=""
```
