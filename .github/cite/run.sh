#!/usr/bin/env bash
#
# Run the OGC CITE "OGC API - Tiles" executable test suite against a shigola
# already serving .github/cite/config.toml, and fail if any assertion fails.
#
# Usage, from the repository root:
#
#   .github/cite/run.sh <tileMatrixSetId> <tileMatrix> <tileRow> <tileCol> [outfile]
#
# The suite runs inside TeamEngine (a container this script starts) and reaches
# shigola on the host, so shigola must already be listening on SHIGOLA_PORT.
#
# Two things about this suite are not obvious and cost an afternoon to find:
#
#  1. Six of its arguments are TEST INPUTS it does not discover for itself —
#     tilematrixsetdefinitionuri, urltemplatefortiles, tilematrix and the row and
#     column bounds. Omit them and three MandatoryCore tests fail with "A tile
#     matrix set definition uri was not found in the test inputs", which reads
#     like a defect in the service under test and is not one.
#
#  2. The EARL report it returns has no summary line. A run that reached nothing
#     at all looks identical to a clean pass unless you count the outcomes, which
#     is why MIN_PASSED exists below.
#
# Pick a row and column inside the tileset's own tileMatrixSetLimits, so the run
# exercises the content checks against a tile that holds something. Measured: the
# suite also passes against an empty tile, so this is about the value of the run
# rather than about making it green.

set -euo pipefail

SCHEME=${1:?usage: run.sh <tileMatrixSetId> <tileMatrix> <tileRow> <tileCol> [outfile]}
TILE_MATRIX=${2:?missing tileMatrix}
TILE_ROW=${3:?missing tileRow}
TILE_COL=${4:?missing tileCol}
OUT=${5:-cite-$SCHEME.xml}

# The suite reports one assertion per abstract test. Requiring a floor means a
# run that silently tested nothing fails instead of passing.
MIN_PASSED=${MIN_PASSED:-15}

# SHIGOLA_PORT, falling back to the pre-rename TEGOLA_PORT so an existing caller
# keeps working. internal/env.Getenv resolves the same way and warns on the
# legacy name; warn here too, or the deprecation is silent in exactly the place
# someone is still using it.
if [ -z "${SHIGOLA_PORT:-}" ] && [ -n "${TEGOLA_PORT:-}" ]; then
	echo "warning: TEGOLA_PORT is deprecated, use SHIGOLA_PORT" >&2
fi
SHIGOLA_PORT=${SHIGOLA_PORT:-${TEGOLA_PORT:-8081}}
TE_PORT=${TE_PORT:-8080}
TE_IMAGE=${TE_IMAGE:-ogccite/ets-ogcapi-tiles10}
TE_NAME=${TE_NAME:-cite-teamengine}

# TeamEngine's REST API is authenticated; these are the image's stock credentials.
TE_AUTH=${TE_AUTH:-ogctest:ogctest}

# How TeamEngine, in a container, addresses shigola on the host. host-gateway is
# resolved by Docker on Linux and already present on Docker Desktop, so the same
# command works on a CI runner and on a developer's machine.
IUT_HOST=${IUT_HOST:-host.docker.internal}
IUT="http://${IUT_HOST}:${SHIGOLA_PORT}/"

log() { echo "==> $*"; }

# --- shigola must be up before the suite is pointed at it ----------------------
log "checking shigola on :${SHIGOLA_PORT}"
for i in $(seq 1 30); do
  if curl -fs -o /dev/null "http://localhost:${SHIGOLA_PORT}/conformance" 2>/dev/null; then
    break
  fi
  if [ "$i" = 30 ]; then
    echo "shigola did not answer on :${SHIGOLA_PORT}" >&2
    exit 1
  fi
  sleep 2
done

# --- TeamEngine --------------------------------------------------------------
if ! docker ps --format '{{.Names}}' | grep -qx "$TE_NAME"; then
  log "starting TeamEngine ($TE_IMAGE)"
  docker rm -f "$TE_NAME" >/dev/null 2>&1 || true
  docker run -d --name "$TE_NAME" \
    -p "${TE_PORT}:8080" \
    --add-host=host.docker.internal:host-gateway \
    "$TE_IMAGE" >/dev/null
fi

log "waiting for TeamEngine on :${TE_PORT}"
for i in $(seq 1 60); do
  if curl -fs -o /dev/null "http://localhost:${TE_PORT}/teamengine/" 2>/dev/null; then
    break
  fi
  if [ "$i" = 60 ]; then
    echo "TeamEngine did not come up" >&2
    docker logs "$TE_NAME" 2>&1 | tail -30 >&2
    exit 1
  fi
  sleep 5
done

# --- run the suite -----------------------------------------------------------
TEMPLATE="http://${IUT_HOST}:${SHIGOLA_PORT}/collections/athens/tiles/${SCHEME}/{tileMatrix}/{tileRow}/{tileCol}"

log "running the suite for ${SCHEME} (tile ${TILE_MATRIX}/${TILE_ROW}/${TILE_COL})"
curl -fsS -u "$TE_AUTH" --max-time 1800 -G \
  --data-urlencode "iut=${IUT}" \
  --data-urlencode "noofcollections=-1" \
  --data-urlencode "tilematrixsetdefinitionuri=http://www.opengis.net/def/tilematrixset/OGC/1.0/${SCHEME}" \
  --data-urlencode "urltemplatefortiles=${TEMPLATE}" \
  --data-urlencode "tilematrix=${TILE_MATRIX}" \
  --data-urlencode "mintilerow=${TILE_ROW}" --data-urlencode "maxtilerow=${TILE_ROW}" \
  --data-urlencode "mintilecol=${TILE_COL}" --data-urlencode "maxtilecol=${TILE_COL}" \
  "http://localhost:${TE_PORT}/teamengine/rest/suites/ogcapi-tiles-1.0/run" > "$OUT"

log "report written to ${OUT}"

# --- score it ----------------------------------------------------------------
MIN_PASSED="$MIN_PASSED" SCHEME="$SCHEME" python3 - "$OUT" <<'PY'
import os, re, sys
from collections import Counter

report = open(sys.argv[1], encoding="utf-8", errors="replace").read()
blocks = re.findall(r"<earl:Assertion.*?</earl:Assertion>", report, re.S)

outcomes = Counter()
failures = []
for b in blocks:
    m = re.search(r'earl:outcome rdf:resource="[^"]*#(\w+)"', b)
    outcome = m.group(1) if m else "unknown"
    outcomes[outcome] += 1

    if outcome == "failed":
        test = re.search(r'<earl:test rdf:resource="([^"]*)"', b)
        why = re.findall(r"<dct:description[^>]*>(.*?)</dct:description>", b, re.S)
        failures.append((
            test.group(1) if test else "?",
            " ".join(re.sub(r"\s+", " ", w).strip() for w in why[:2])[:400],
        ))

scheme = os.environ["SCHEME"]
print(f"{scheme}: " + ", ".join(f"{n} {k}" for k, n in sorted(outcomes.items())))

for test, why in failures:
    print(f"  FAILED {test}\n         {why}")

# A skipped test is not a failure: conf/dataset-tilesets is a conformance class
# this service deliberately neither implements nor declares.
minimum = int(os.environ["MIN_PASSED"])
passed = outcomes.get("passed", 0)

if failures:
    sys.exit(f"{scheme}: {len(failures)} assertion(s) failed")

if passed < minimum:
    sys.exit(
        f"{scheme}: only {passed} assertions passed, expected at least {minimum}. "
        "A suite that tested almost nothing reports no failures, so this is treated as one."
    )

print(f"{scheme}: OK")
PY
