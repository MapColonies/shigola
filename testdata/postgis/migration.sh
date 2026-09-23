#!/bin/bash
# pipefail matters as much as -e here: every command below runs through run(),
# which pipes into sed, and a pipeline's status is its last command's. Without
# it a failed pg_restore is reported as sed's success, the script prints
# "Migration completed successfully", the container exits 0, and CI runs the
# whole suite against an empty database.
set -eo pipefail

# indent command output by 4 spaces
run() {
  "$@" 2>&1 | sed 's/^/    /'
}

export PGHOST="postgis"
export PGPORT="5432"
export PGUSER="postgres"
export PGPASSWORD="postgres"
export PGDATABASE="postgres"

# The fixture database and the role used to test permission errors. The archive
# restored below was dumped from a database of this name, so pg_restore -C
# recreates it -- see testdata/postgis/generate-dump.sh for why the name lives
# inside the archive rather than being a reference to it.
DB="shigola"
NO_ACCESS_ROLE="shigola_no_access"

echo "Dropping existing '$DB' database (if any)..."
run psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -c "DROP DATABASE IF EXISTS $DB;"

echo "Restoring database from dump..."
run pg_restore -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -C /testdata/postgis/shigola.dump

echo "Dropping and creating role '$NO_ACCESS_ROLE'..."
run psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -c "DROP ROLE IF EXISTS $NO_ACCESS_ROLE; CREATE ROLE $NO_ACCESS_ROLE LOGIN PASSWORD 'postgres';"

echo "Applying SQL files from /testdata with prefix 'postgis-'..."
for sqlfile in /testdata/postgis/postgis-*.sql; do
  echo "Applying $sqlfile..."
  run psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$DB" -f "$sqlfile"
done

echo "Migration completed successfully."
