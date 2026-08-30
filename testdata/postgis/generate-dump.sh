#!/usr/bin/env bash
#
# Regenerate testdata/postgis/shigola.dump, the PostGIS fixture CI restores.
#
# Why this script has to exist at all: a pg_dump custom-format archive records
# the name of the database it was dumped from in its header, and `pg_restore -C`
# creates *that* name. The fixture database name is therefore data inside the
# archive, not a reference to it -- which is why renaming every mention of
# "tegola" in the tree never renamed the database, and why regenerating the
# archive from a database that is genuinely called "shigola" is the only fix.
#
# The same rebuild is the moment the Athens OSM extract can be added, so the
# PostGIS fixture carries the layers the OGC CITE suite exercises (see
# .github/cite/config.toml) instead of only the GeoPackage carrying them.
#
# Sources, both checked in:
#   --from   the current dump, for the legacy fixture tables (hstore_test,
#            ne_10m_land_scale_rank, null_geom_test, osm_buildings_test,
#            three_d_test) and the as_numeric/tilebbox functions the provider
#            tests call. Their contents predate this repository and there is no
#            higher-level source to rebuild them from, so they are carried
#            forward rather than regenerated.
#   the GeoPackage at testdata/postgis/athens-osm-20170921.gpkg, for the three
#            Athens layers. It sat under provider/gpkg/testdata until that
#            provider was deleted (MAPCO-11488) and moved here with it: shigola
#            cannot read a GeoPackage any more, but this script does not ask it
#            to -- GDAL converts the file, and it is the only provenance the
#            Athens layers have.
#
# "Reproducible" here means the same logical content every run, not identical
# bytes: pg_dump stamps a creation time into the archive header, so two runs
# always differ in those bytes.
#
# Usage, from the repository root:
#
#   testdata/postgis/generate-dump.sh
#   testdata/postgis/generate-dump.sh --from old.dump --out new.dump
#   testdata/postgis/generate-dump.sh --help
#
# Requires Docker and nothing else -- no local postgres or GDAL install.

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

# Constants, deliberately not overridable. migration.sh, .github/workflows and
# the Go test defaults all hardcode "shigola", so a DB_NAME override here would
# only produce a fixture nothing can restore under the name it expects.
DB_NAME=shigola

# Match the image docker-compose.yml runs, so the archive version this produces
# is one the pg_restore in CI can read. A newer pg_dump writes archives an older
# pg_restore refuses.
PG_IMAGE=postgis/postgis:12-3.0-alpine

# Pinned by digest, not by tag. "alpine-small-latest" is a moving tag, and
# ogr2ogr's type mapping and PROMOTE_TO_MULTI behaviour are what decide the
# shape of the Athens tables -- a floating GDAL would make this script's output
# depend on the day it ran, which is exactly the reproducibility it claims.
# This digest is GDAL 3.14.0dev (2026-08-18). Bumping it is a deliberate act:
# regenerate and check the resulting table definitions before committing.
GDAL_IMAGE=ghcr.io/osgeo/gdal@sha256:01ae355051f63f17b8f1ffd5486331b4996a7b9f618c680e418a7228b236cc55

GPKG=$REPO_ROOT/testdata/postgis/athens-osm-20170921.gpkg
FROM_DUMP=$REPO_ROOT/testdata/postgis/shigola.dump
OUT_DUMP=$REPO_ROOT/testdata/postgis/shigola.dump

while [ $# -gt 0 ]; do
	case $1 in
	--from)
		FROM_DUMP=${2:?--from needs a path}
		shift 2
		;;
	--out)
		OUT_DUMP=${2:?--out needs a path}
		shift 2
		;;
	-h | --help)
		sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//;$d'
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
done

[ -f "$FROM_DUMP" ] || { echo "source dump not found: $FROM_DUMP" >&2; exit 1; }
[ -f "$GPKG" ] || { echo "geopackage not found: $GPKG" >&2; exit 1; }

CONTAINER=shigola-fixture-build-$$
WORK=$(mktemp -d)
cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
	rm -rf "$WORK" "$OUT_DUMP.tmp"
}
trap cleanup EXIT INT TERM

say() { printf '==> %s\n' "$*"; }

# --- 1. a disposable postgres -------------------------------------------------
#
# No published ports: everything below reaches it through `docker exec`, so a
# developer running this does not collide with whatever is already on 5432.
say "starting $PG_IMAGE as $CONTAINER"
docker run -d --name "$CONTAINER" \
	-e POSTGRES_USER=postgres \
	-e POSTGRES_PASSWORD=postgres \
	-e POSTGRES_DB=postgres \
	"$PG_IMAGE" >/dev/null

# Gate on TCP, not on the Unix socket. The image's entrypoint runs initdb and the
# PostGIS init scripts against a temporary server started with listen_addresses='',
# then shuts it down and starts the real one. A Unix-socket pg_isready passes
# against that temporary server, so work started on it dies mid-statement with
# "terminating connection due to administrator command". Only TCP distinguishes
# the two.
for _ in $(seq 1 120); do
	if docker exec "$CONTAINER" pg_isready -h 127.0.0.1 -p 5432 -U postgres -d postgres >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
docker exec "$CONTAINER" pg_isready -h 127.0.0.1 -p 5432 -U postgres -d postgres >/dev/null

psql_db() { docker exec -i "$CONTAINER" psql -v ON_ERROR_STOP=1 -U postgres -d "$DB_NAME" "$@"; }

# --- 2. the database, under the name we actually want -------------------------
say "creating database $DB_NAME"
docker exec "$CONTAINER" psql -v ON_ERROR_STOP=1 -U postgres -d postgres \
	-c "DROP DATABASE IF EXISTS $DB_NAME;" -c "CREATE DATABASE $DB_NAME;" >/dev/null

# --- 3. the legacy fixture, carried forward -----------------------------------
#
# Restored WITHOUT -C: -C would obey the name in the source archive's header,
# which is the very thing being changed. Restoring into a database this script
# named leaves the old name with nowhere to survive.
#
# --no-owner/--no-privileges drop the "arolek" role the 2020 archive was dumped
# under; nothing in the tests depends on ownership, and requiring that role to
# exist would be a second thing to keep in sync.
#
# pg_restore reports errors on stderr and still exits 0, so its output is
# inspected rather than its status trusted. No error here is expected: a clean
# restore of this archive emits none. In particular spatial_ref_sys does not
# collide with the rows CREATE EXTENSION postgis installs -- PostGIS registers
# that table with pg_extension_config_dump filtered to non-stock rows, and the
# fixture adds no custom SRIDs, so the archive carries zero of them.
#
# Matched on the "pg_restore: error:" prefix rather than on the word: the run's
# closing "pg_restore: warning: errors ignored on restore: N" line contains
# "errors" and would otherwise trip the guard on every failing run, reporting
# the summary instead of the cause.
say "restoring legacy fixture from $(basename "$FROM_DUMP")"
docker cp "$FROM_DUMP" "$CONTAINER:/tmp/source.dump"
restore_log=$WORK/restore.log
docker exec "$CONTAINER" pg_restore --no-owner --no-privileges \
	-U postgres -d "$DB_NAME" /tmp/source.dump >"$restore_log" 2>&1 || true

if grep -q '^pg_restore: error:' "$restore_log"; then
	echo "pg_restore failed to restore $FROM_DUMP cleanly:" >&2
	grep '^pg_restore: error:' "$restore_log" >&2
	exit 1
fi

# --- 4. the Athens layers -----------------------------------------------------
#
# Only the three layers .github/cite/config.toml serves. The GeoPackage holds
# nineteen; importing the rest would grow the fixture for data no test reads.
#
# -nlt PROMOTE_TO_MULTI: two of these tables declare a single-part geometry type
# in gpkg_geometry_columns and then store multi-part geometries, which PostGIS
# rejects against a typed column. Promoting makes the declared type true.
#
# -preserve_fid: without it ogr2ogr lets the serial assign new ids, so feature
# ids would drift from the GeoPackage's on every regeneration. The conformance
# suite reads feature ids, so they are fixture content, not an implementation
# detail.
#
# land_polygons is the exception to -lco FID=fid: it already has its own "fid"
# attribute column distinct from its primary key, so it keeps ogr2ogr's default
# ogc_fid and both columns survive, exactly as they exist in the GeoPackage.
say "converting Athens layers with ogr2ogr"
docker run --rm \
	-v "$(dirname "$GPKG"):/gpkg:ro" \
	-v "$WORK:/out" \
	"$GDAL_IMAGE" sh -c '
set -e
gpkg=/gpkg/$1
common="-lco GEOMETRY_NAME=geom -lco SCHEMA=public -lco CREATE_SCHEMA=OFF
        -lco DROP_TABLE=IF_EXISTS -lco SPATIAL_INDEX=GIST
        -a_srs EPSG:4326 -preserve_fid"
ogr2ogr -f PGDump /out/10-land_polygons.sql "$gpkg" land_polygons \
	$common -nlt PROMOTE_TO_MULTI -nln land_polygons
ogr2ogr -f PGDump /out/20-roads_lines.sql "$gpkg" roads_lines \
	$common -lco FID=fid -nlt PROMOTE_TO_MULTI -nln roads_lines
ogr2ogr -f PGDump /out/30-places_points.sql "$gpkg" places_points \
	$common -lco FID=fid -nln places_points
' sh "$(basename "$GPKG")" >/dev/null

for sql in "$WORK"/[0-9]*-*.sql; do
	say "loading $(basename "$sql")"
	psql_db -q -f - <"$sql" >/dev/null
done

# --- 5. the archive -----------------------------------------------------------
#
# -Fc so migration.sh can keep using pg_restore, and so the archive stays
# roughly a tenth the size of the equivalent plain SQL.
# Written beside the target and moved into place only once pg_dump has
# succeeded. --from and --out default to the same path, so writing in place
# would let a failed run truncate the only copy of the fixture.
say "dumping $DB_NAME"
docker exec "$CONTAINER" pg_dump -Fc --no-owner --no-privileges \
	-U postgres -d "$DB_NAME" -f /tmp/out.dump
docker cp "$CONTAINER:/tmp/out.dump" "$OUT_DUMP.tmp"
mv "$OUT_DUMP.tmp" "$OUT_DUMP"

say "wrote $OUT_DUMP"
docker exec "$CONTAINER" pg_restore -l /tmp/out.dump | sed -n '1,12p'
