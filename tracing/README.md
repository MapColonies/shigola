# Tracing

Shigola can export OpenTelemetry traces over OTLP, to Grafana Tempo or to any
collector that speaks it. It is **off by default** and configured entirely in
its own `[tracing]` section.

Tracing runs *alongside* the Prometheus observer rather than replacing it.
Metrics remain the observer's job: nothing here registers a collector, and
nothing here installs an OTEL meter provider, so a build with tracing enabled
publishes exactly the metric families it published before. `atlas`'s
`TestMetricsAreUnaffectedByTracing` asserts that rather than leaving it to be
believed.

## What tracing answers that metrics cannot

A histogram can say a tile took 300ms. It cannot say whether that was the
provider's SQL, one slow cache tier, or the gzip — and on a layered cache it
cannot say *which* tier. A span tree can, because it is per request rather than
aggregated:

```
GET /collections/:collection_id/tiles/:tile_matrix_set_id/:tile_matrix/:tile_row/:tile_col
├── cache.Get                       the chain as a whole
│   ├── cache.tier.Get  tier=hot        miss
│   └── cache.tier.Get  tier=durable    miss
├── atlas.Encode                    map, scheme and tile on the span
│   └── provider.MVTForLayers       the ST_AsMVT round trip
└── cache.tier.Set      tier=durable    the write, off the response path
```

## Configuration

```toml
[tracing]
enabled = true
exporter = "otlp_grpc"                 # or "otlp_http"
endpoint = "tempo.observability:4317"
insecure = true
sample_ratio = 0.01
service_name = "shigola"
timeout_ms = 10000

  [tracing.headers]
  x-scope-orgid = "tenant-a"
```

| Key | Default | Meaning |
|:---|:---|:---|
| `enabled` | `false` | Absent or false installs the no-op backend and dials nothing. |
| `exporter` | `otlp_grpc` | OTLP transport. Tempo listens for gRPC on 4317 and HTTP on 4318, and a collector in front of it may accept only one. |
| `endpoint` | *(SDK default)* | Collector address. Empty hands the decision to the OTEL SDK, which reads `OTEL_EXPORTER_OTLP_ENDPOINT` and its per-signal siblings. |
| `insecure` | `false` | Export without TLS. Normal for a collector reached over the pod network, wrong across anything else. |
| `sample_ratio` | `0.01` | Fraction of traces *this service starts* to record. See below. |
| `service_name` | `shigola` | `service.name` on every span. Set it per deployment if several shigolas report to one Tempo. |
| `timeout_ms` | `10000` | Bounds one export attempt. |
| `[tracing.headers]` | *(none)* | Sent with each export request — an auth header, a Tempo tenant id. |

`endpoint` is the one place shigola does **not** use its own `SHIGOLA_*`
environment variable convention. The OTLP exporters read the standard
`OTEL_EXPORTER_OTLP_*` variables themselves, and a sidecar collector is
normally configured that way for every service in a cluster at once;
substituting a shigola-specific default would take that away.

An unusable `[tracing]` section is a startup error, and it is checked whether or
not tracing is enabled — so a typo waiting in a switched-off section is found
now rather than on the day someone switches it on.

## Sampling, and why the default is 1%

`sample_ratio` defaults to `0.01`, deliberately not `1.0`.

A tile server answers thousands of requests a second, and **each tile request
produces a span per cache tier plus the provider query and the encode**. Head
sampling everything therefore multiplies request rate by the span tree's width
before it reaches the exporter, the network and Tempo's ingesters: a service at
2 000 req/s with a two-tier cache emits on the order of 10 000 spans/s, tens of
megabytes a minute on the wire, and a Tempo bill and ingester memory footprint
to match. The spans are also produced on the request path, so at full sampling
the attribute-building and export queueing become part of tile latency rather
than a background cost.

1% keeps that bounded while still yielding hundreds of complete traces a
minute, which is more than enough to characterise a latency problem. Raise it
temporarily while chasing something specific; `1.0` is reasonable in a staging
environment or a deployment serving single-digit requests per second, and
almost never in production.

The sampler is **parent-based**: an upstream decision is always followed, and
`sample_ratio` applies only to traces shigola roots itself. Without that a
gateway that chose to sample a request would hand it to a shigola that dropped
its half of the trace 99 times out of 100, leaving a hole exactly where the tile
was served. This is also why `sample_ratio = 0` is a meaningful setting rather
than a synonym for `enabled = false`: record nothing this service starts, but
keep every trace something upstream already decided to keep.

## Propagation

Incoming W3C `traceparent`/`tracestate` and `baggage` are extracted per request,
so a request arriving from an upstream service continues that trace rather than
rooting a sibling. The context is threaded from the handler through the encode,
the provider and every cache tier, which is what puts them all in one trace.

On startup the backend is published as OTEL's process-wide tracer provider and
text-map propagator. That is what makes an OTEL-instrumented client inject this
service's trace context into an outgoing call without further wiring.

**Not yet instrumented:** the outgoing calls shigola itself makes — PostGIS
queries over pgx, and the S3, GCS and Azure Blob cache tiers — do not currently
inject trace context or emit client spans of their own. Their latency is visible
as the duration of the `provider.MVTForLayers` and `cache.tier.*` spans that
contain them, but a trace does not continue into the database or the object
store. Adding that is a separate change per client library.

## Costs when disabled

Nothing. A disabled config returns the no-op backend before an exporter is
dialled or a batch processor is started, and that backend's `Instrumented*`
methods return their argument — so a process with tracing off carries **no
decorator at all**, rather than one that starts a discarded span per cache read.

## Shutdown

Spans are exported in batches, so up to a batch interval's worth are in memory
at any moment — and the traces most worth having are usually the ones from just
before a shutdown. `shigola serve` and `shigola cache seed|purge` therefore
flush on the way out, after the cache write pool has drained so that the drain's
own spans are included. The flush is bounded at two seconds: a collector that
has gone away does not fail fast, and an unbounded flush would turn a missing
trace into a failed rolling deploy.

## Spans and attributes

| Span | Where it starts |
|:---|:---|
| `GET <route>` | per request, named for the route pattern rather than the path — a tile server's paths are unbounded by construction |
| `cache.Get` / `cache.Set` / `cache.Purge` | the cache as a whole |
| `cache.tier.Get` / `cache.tier.Set` / `cache.tier.Purge` | one tier of a chain, carrying `shigola.cache.tier` |
| `atlas.Encode` | the encode, carrying map, scheme and tile coordinates |
| `provider.MVTForLayers` | the provider query, carrying `shigola.provider` and `shigola.layer_count` |

Attributes are in shigola's own `shigola.*` namespace: the OTEL semantic
conventions have nothing for a tile or a cache tier, and an unprefixed key
risks colliding with a convention that later does.

A failed operation records the error on its span. The span's *status* is set to
error only when the failure is shigola's own — a read that failed because the
client disconnected is recorded but not marked failed, which is the same line
`shigola_cache_tier_errors_total` draws. A trace saying a tier failed while that
counter says it did not would be worse than either signal alone.

Note that a cache read failure is still a **miss, not an error**, to the caller:
a dead tier is logged, counted, spanned and skipped, and a chain where every
tier failed returns a miss. Tracing does not change that, and
`TestTracedCacheGetIsTransparent` pins it.
