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

What tracing does add to both other signals is its ids: on log records, and as
exemplars on the duration histograms — see
[Correlating logs with traces](#correlating-logs-with-traces) and
[Correlating metrics with traces](#correlating-metrics-with-traces). Neither
adds a metric family or a log field outside a trace.

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

## Correlating logs with traces

Log records written while serving a traced request carry that request's ids as
top-level fields, so a trace in Tempo reaches the log lines it produced and a
log line reaches its trace (MAPCO-11494):

```json
{"time":"...","level":"ERROR","msg":"cache/multi: tier (redis) get: dial tcp: connection refused",
 "shigola":{"version":"1.4.0","pid":1,"rev":"9f3c1ab"},
 "trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
```

The keys are `internal/log.TraceIDKey` and `SpanIDKey`. `internal/log.Handler`
adds them from the span context it finds on the context it is handed, so a call
site correlates exactly when it logs through one of the `*fContext` helpers with
the request's context.

**Which sites those are.** Every log site on the request path whose signature
has a context: the layered cache's tier read and promotion failures, the GCS
backend's per-key lines, pgx's statement warnings and errors, the PostGIS SQL
debug output, and `server/ogc`'s response and cache failures.

Three ERROR-level sites are reached mid-request and still carry nothing, so an
uncorrelated line during a request is possible rather than a contradiction:

- `provider.NewTileForGrid` — "tile grid %v has no EPSG code";
- `tile_t.Extent` — "Unsupported tile SRID" and "Could not generate valid
  extent for tile", reached from `provider/postgis/util.go`.

Both are exported signatures with no context to take, and `Extent` is on the
`provider.Tile` interface, so threading one through is an interface change
rather than a call-site change. Both also report a *grid misconfiguration*,
which fails identically for every request to that map rather than saying
anything about one — so what a trace would add is the least here of anywhere.
(`provider.NewTile` logs the same class of failure but is only reached at
provider construction, never per request.)

Three properties of this are load-bearing.

**Top level, not under the `shigola` group.** The group is attached as a single
grouped attribute rather than with `WithGroup`, because an open group qualifies
everything after it and would emit `shigola.trace_id` — not a name any log
pipeline looks for. `internal/log.ServiceAttrs` returns an `slog.Attr` precisely
so that placement cannot be undone by a caller reaching for `WithGroup`.

**The span, not just the trace.** The ids are the innermost span active where
the line was written, so a tier read failure hangs off `cache.Get`, a pgx
warning off `postgis.query`. In Tempo the line lands on the span that produced
it.

**Sampling is not consulted.** At the default `sample_ratio = 0.01` most
requests are unsampled, and their log lines still carry ids. That is deliberate:
the ids group one request's lines together whether or not a trace was kept, and
suppressing them would leave 99% of requests uncorrelated *with themselves*. The
cost is that a "view trace" link resolves only for sampled traces — raise
`sample_ratio` while chasing something specific.

Two consequences worth knowing:

- **Detached cache writes correlate too.** A write handed to the bounded write
  pool completes after the response, but the pool derives its context with
  `context.WithoutCancel`, which drops cancellation and keeps values — so a
  write failure still names the request that caused it, even though the span it
  names has already ended.
- **Lines written outside a request carry nothing.** Startup, configuration,
  shutdown and the pool-level saturation warnings have no request context by
  definition, and no empty fields are added for them. Nor does anything logged
  when `[tracing]` is disabled: there is no span, so there are no ids.
- **The `stack` field moved.** `ServiceAttrs` is what took the process identity
  out of an open group, and an ERROR record's stack trace was inside that group
  too — it is now `stack` at the top level rather than `shigola.stack`. Anything
  parsing that path needs updating; nothing else about the record changed.

## Correlating metrics with traces

Cache, per-tier and HTTP duration observations carry the active trace and span
as a **Prometheus exemplar**, so a slow histogram bucket in Grafana links
straight to the trace that landed in it (MAPCO-11496):

```
shigola_cache_tier_duration_seconds_bucket{tier="durable",sub_command="get",le="0.5"} 3 # {trace_id="4bf92f3577b34da6a3ce929d0e0e4736",span_id="00f067aa0ba902b7"} 0.41 1.7e+09
```

The labels are `internal/log.TraceIDKey` and `SpanIDKey` — the same constants the
log records carry, deliberately. Logs-to-traces and metrics-to-traces are wired
up separately in Grafana (a derived field on the Loki datasource, an exemplar
link on the Prometheus one) and **both fail silently on a wrong name**, so one
pair of constants is what stops a rename from fixing one surface and breaking
the other.

Which observations carry one: `shigola_cache_duration_seconds`,
`shigola_cache_tier_duration_seconds` and `shigola_api_duration_seconds`. The
size histograms and the counters do not — the ticket asked for the duration
families, and an exemplar is worth having where there is a spike to click.

**The exposition format is not optional.** OpenMetrics is the only format that
encodes exemplars; the classic Prometheus text format has no syntax for them and
drops them without a word. The metrics route therefore negotiates OpenMetrics
(`observability/prometheus.metricsHandler`), which Prometheus offers in its own
scrape `Accept` header. The vendored `expfmt` negotiates version `0.0.1` only —
enough, because Prometheus offers both `1.0.0` and `0.0.1`, but a hand-rolled
scrape offering `1.0.0` alone silently gets the classic format and no exemplars.

Storing them is a server-side switch as well: Prometheus needs
`--enable-feature=exemplar-storage`, and Mimir its equivalent, or the exemplars
are scraped and discarded.

Three properties of this are load-bearing.

**Sampling *is* consulted — the opposite of the log records above.** An exemplar
is only ever a link, and prometheus keeps one per bucket, overwritten by the next
observation to land there. So the stored exemplar is almost always the most
recent observation, and at `sample_ratio = 0.01` the most recent observation is
almost never sampled: unfiltered, clicking a bucket would open nothing roughly
99 times out of 100. A log line's trace id keeps its consolation use when the
trace was dropped — it still groups that request's lines — and an exemplar has
none, which is why the same span context is filtered here and not there.

**The span, not just the trace.** The observation is made *inside* the
operation's span because the tracing wrappers are installed outside the metric
ones at both seams — `atlas.instrumentCache` and `server.NewRouter`, each with a
comment saying so. A slow bucket on the per-tier histogram therefore names the
tier read that was slow, not the request as a whole. Inverting either order
leaves the span tree unchanged, so it is pinned by the exemplar instead:
`atlas.TestTierExemplarNamesTheTierSpan` and
`server.TestRequestExemplarNamesTheRequestSpan` both fail on it, and say which
way round it went wrong.

**Nothing is attached outside a trace.** `exemplarFrom` returns nil, which is
the client's own "no exemplar" signal, so an untraced observation is recorded
exactly as a plain `Observe` would record it — no empty label, nothing for the
exposition to carry. The label set is 63 of the 128 runes the client allows;
past that limit `ObserveWithExemplar` panics rather than returning an error,
which `TestExemplarFitsTheRuneLimit` guards by making a real observation.

Two consequences worth knowing:

- **`le` label values changed.** Under OpenMetrics a bucket boundary that would
  otherwise look like an integer is written with a trailing `.0`, and a label
  value is part of a series' identity — so `le="1"` is now `le="1.0"`. The
  affected boundaries are 1, 2.5 and 5 on the cache families and 1, 5 and 10 on
  the HTTP one. Anything pinning an exact `le` — a recording rule, a panel
  showing one bucket — has to be checked against the new spelling. This was the
  price of exemplars being scrapeable at all.
- **Pushed metrics carry no exemplars.** A deployment using `push_url` pushes
  through the classic text format to a Pushgateway, which has no notion of
  exemplars. Everything above applies to scraped deployments only.

## Costs when disabled

Nothing. A disabled config returns the no-op backend before an exporter is
dialled or a batch processor is started, and that backend's `Instrumented*`
methods return their argument — so a process with tracing off carries **no
decorator at all**, rather than one that starts a discarded span per cache read.

Log correlation costs nothing when disabled either, and it is not gated on the
config: the handler reads the context whether or not tracing is on. With no span
there is nothing to read.

`internal/log`'s three benchmarks are meant to be read together, and are the
evidence for that:

| Benchmark | What it measures | ns/op | allocs/op |
|:---|:---|---:|---:|
| `BenchmarkHandleWithoutTheWrapper` | the base JSON handler alone | 325–375 | 0 |
| `BenchmarkHandleWithoutATrace` | this package's handler, nothing in the context | 339–345 | 0 |
| `BenchmarkHandleInATrace` | the same, with ids to add | 557–568 | 2 |

The first two overlap: the level comparison and the context lookup the wrapper
adds on an untraced record are inside the base handler's own run-to-run spread,
at no allocations. Re-measure with
`go test -bench BenchmarkHandle -benchtime 500000x -count 3 ./internal/log/`.

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
