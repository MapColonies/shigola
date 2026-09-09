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
│   └── provider.MVTForLayers       the SQL assembly and the query
│       └── postgis.query           the round trip: statement, server, duration
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
| `endpoint` | *(SDK default)* | Collector address, as either `host:port` or a full URL. Empty hands the decision to the OTEL SDK, which reads `OTEL_EXPORTER_OTLP_ENDPOINT` and its per-signal siblings. |
| `insecure` | `false` | Export without TLS. Normal for a collector reached over the pod network, wrong across anything else. Ignored when `endpoint` is a URL — the scheme has already said. |
| `sample_ratio` | `0.01` | Fraction of traces *this service starts* to record. See below. |
| `service_name` | `shigola` | `service.name` on every span. Set it per deployment if several shigolas report to one Tempo. |
| `timeout_ms` | `10000` | Bounds one export attempt. |
| `[tracing.headers]` | *(none)* | Sent with each export request — an auth header, a Tempo tenant id. |

### Endpoint shapes

Both of these work:

```toml
endpoint = "tempo.observability:4317"                      # host and port
endpoint = "https://collector.example.io/v1/traces"        # a full URL
```

They are not interchangeable underneath — the OTLP exporters take them through
different options, `WithEndpoint` and `WithEndpointURL` — and getting it wrong
used to be silent. Passing a URL where a host was expected does not fail: the
exporter treats the whole string as a host, percent-encodes it, prefixes a
scheme and appends the signal path, then fails on *every export* with

```
traces export: parse "http://https:%2F%2Fcollector.example.io%2Fv1%2Ftraces/v1/traces":
invalid port ":%2F%2Fcollector.example.io%2Fv1%2Ftraces" after host
```

A deployment ran like that: healthy by every other signal, exporting nothing.
Shigola now picks the right option from the scheme, and refuses at **startup**
any endpoint that cannot work — a path with no scheme (`tempo:4318/v1/traces`),
a scheme that is not http or https, a non-numeric port, or an `https` endpoint
with `insecure = true`, which are asking for opposite things.

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

**What actually carries it.** Installing the global propagator is enough for any
client library that reads it, and shigola's outgoing calls divide on whether
theirs does:

- **GCS cache** — carries it. `storage.NewClient` is built on
  `google.golang.org/api`'s transport, which wraps itself in
  `otelhttp.NewTransport`; that reads OTEL's globals, so once tracing is
  installed GCS reads and writes inject `traceparent` and appear as **HTTP
  client spans** under their `cache.tier.*` parent.

  Two details worth knowing. The spans are the transport's, at HTTP level —
  `cloud.google.com/go/storage`'s own operation spans are gated behind
  `GO_STORAGE_DEV_OTEL_TRACING=true` while that feature is experimental, and
  shigola does not set it. And the transport is constructed *before* `Install`
  runs — `register.Cache` happens earlier in startup than the tracing setup —
  so this works only because OTEL's global propagator is a delegating wrapper
  rather than a value captured at construction. That indirection is load-bearing
  and easy to break, so `TestInstalledPropagatorReachesAnOutgoingClient` pins it.

  The transport reads the global *meter* provider too, which shigola leaves as a
  no-op, so none of this adds metrics.
- **PostGIS** — does not. pgx is configured with a `tracelog.TraceLog`, which
  logs statements; it is not an OTEL tracer. Note the distinction: shigola does
  emit a `postgis.query` client span *around* the call, so the query is visible
  and timed — what does not happen is the trace continuing into the database's
  own instrumentation.
- **S3 cache** — does not. It is on aws-sdk-go v1, which has no OTEL hook.
- **Azure Blob cache** — does not. The Azure SDK has its own tracing
  abstraction rather than OTEL's.

For the three that do not, the call's latency is still visible as the duration of
the `provider.MVTForLayers` or `cache.tier.*` span containing it — but the trace
stops there rather than continuing into the database or the object store.
Extending it is a separate change per client library.

## Costs when disabled

Nothing. A disabled config returns the no-op backend before an exporter is
dialled or a batch processor is started, and that backend's `Instrumented*`
methods return their argument — so a process with tracing off carries **no
decorator at all**, rather than one that starts a discarded span per cache read.

## When it is not working

Two failures used to be quiet, and are not any more.

**A misconfigured endpoint fails at startup**, not per export. `Config.Validate`
checks the shape, so a bad value is a startup error naming the key rather than
a running server that exports nothing.

**OTEL's own errors are logged at ERROR.** They were at INFO, which is the more
interesting failure: OTEL's default error handler calls the standard library's
`log.Print`, and `slog.SetDefault` redirects the standard library's default
logger through the slog handler *at INFO* — so a dead collector announced itself
at INFO and a service running at `--log-level WARN` saw nothing at all. `Install`
now sets an error handler of its own, and bridges OTEL's internal logger into
slog so its diagnostics arrive structured and levelled rather than as plain text
on stderr.

So: if tracing is enabled and no traces arrive, look for `ERROR` lines prefixed
`tracing:`.

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
| `postgis.query` | the `ST_AsMVT` round trip, so the database's own time is separable from the provider's (MAPCO-11620) |

Attributes are in shigola's own `shigola.*` namespace: the OTEL semantic
conventions have nothing for a tile or a cache tier, and an unprefixed key
risks colliding with a convention that later does.

### The query span

`postgis.query` is the exception to that: it uses the OTEL **database**
conventions, which do exist and which Grafana and Tempo render specially.

| Attribute | What |
|:---|:---|
| `db.query.text` | the statement, parameterised |
| `db.system.name` | `postgresql` |
| `db.namespace` | the database name |
| `server.address`, `server.port` | the server the query went to |
| `shigola.db.query_truncated` | present when the statement hit the size cap |

Its **duration is the database's own time**, where the enclosing
`provider.MVTForLayers` also covers assembling the SQL. That is the split worth
having: a tile that took 300ms because the query took 295ms is a database
problem, and one where the query took 5ms is not.

Three things about `db.query.text` worth knowing:

- **It carries no parameter values.** Configured query parameters are passed to
  pgx as arguments, so the statement holds `$1` placeholders. That is what the
  OTEL conventions ask for, and it is what keeps client-supplied values out of
  traces.
- **It carries no credentials.** The server attributes are built from three
  fields of the connection config — host, port, database — and deliberately not
  from the user or the password it also holds.
- **It is capped at 8KiB**, cut on a UTF-8 boundary, with
  `shigola.db.query_truncated` set when it cuts. A tile query is one statement
  per layer, unioned, with the tile's tokens substituted, so a wide map
  produces a large one — and OTEL's SDK applies no value-length limit of its
  own by default.

The statement is built only for a span that is being recorded, so tracing off
or a sampling miss costs no string work. `shigola_mvt_provider_query_seconds`
is unaffected: the span opens before the histogram's clock starts.

A failed operation records the error on its span. The span's *status* is set to
error only when the failure is shigola's own — a read that failed because the
client disconnected is recorded but not marked failed, which is the same line
`shigola_cache_tier_errors_total` draws. A trace saying a tier failed while that
counter says it did not would be worse than either signal alone.

Note that a cache read failure is still a **miss, not an error**, to the caller:
a dead tier is logged, counted, spanned and skipped, and a chain where every
tier failed returns a miss. Tracing does not change that, and
`TestTracedCacheGetIsTransparent` pins it.
