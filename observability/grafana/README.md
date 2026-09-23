# Grafana dashboard

`shigola-metrics.json` is a Grafana dashboard covering the metrics this package
emits, the write-pool and promotion collectors in `atlas/cache_collectors.go`,
the PostGIS provider collectors in `provider/postgis`, and the spans in
`tracing/`.

Import it with **Dashboards → New → Import**, or:

```sh
curl -s -X POST http://<grafana>/api/dashboards/db \
  -H 'Content-Type: application/json' \
  --data-binary "{\"dashboard\": $(cat shigola-metrics.json), \"overwrite\": true}"
```

The dashboard uid is `shigola-metrics`; re-importing updates it in place.

## Datasources

Prometheus and Tempo are dashboard *variables* (`ds_prom`, `ds_tempo`), not
hardcoded uids, so the dashboard is portable between stacks. Grafana picks the
default of each type on import; change them in the dashboard's variable row.

## Things the panels encode that the metric names do not

- **`shigola_cache_*` and `shigola_cache_tier_*` must never be summed together.**
  A whole-cache hit is one tile served from somewhere in the chain; tier hits are
  per-tier lookups, several per request. They are separate rows here for that
  reason.
- **`shigola_cache_tier_errors_total` is the only evidence a tier is broken.** A
  read failure is logged, counted and turned into a miss, so the tile still
  serves, the status does not change and latency barely moves. It has its own
  stat in the Overview row rather than being buried in the tier row.
- **`writes_timed_out_total` is also counted in `writes_failed_total`** — the
  outcomes panel says so, because adding them double-counts.
- **The API duration buckets are `.25/.5/1/2.5/5/10`s.** Every quantile below
  250ms reads as 250ms, so the latency panels are a band, not a number.
- **Layers are keyed `<map>:<layer>`**, matching the OGC collection id, because
  `layer_name` alone is ambiguous once more than one map is served. A whole-map
  tile carries no layer and renders as the bare `<map>`.
