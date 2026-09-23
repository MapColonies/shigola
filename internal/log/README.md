internal/log
============

Shigola's logging: the logger the binaries install as slog's default, the
handler behind it, and the package-level helpers every other package logs
through.

## The record

Every record is one line of JSON in the MapColonies format — the shape
[js-logger](https://github.com/MapColonies/infra-packages/tree/master/packages/js-logger)
writes on its default path, so the same collector, dashboards and queries work
across shigola and the JS services (MAPCO-11544):

```json
{"time":1790161525425,"level":"error","msg":"config file at location (/nope.toml) not found",
 "pid":83,"hostname":"web-01","version":"v1.4.0","rev":"9f3c1ab",
 "err":{"type":"*errors.errorString","message":"config file at location (/nope.toml) not found","stack":"goroutine 1 [running]:\n..."}}
```

| Key | Always | What |
|:---|:---:|:---|
| `time` | yes | integer milliseconds since the Unix epoch |
| `level` | yes | `debug`, `info`, `warn` or `error` — pino's labels, lowercase |
| `msg` | yes | the message |
| `pid` | yes | the process id |
| `hostname` | yes | the host's name; absent only if the OS will not say |
| `version`, `rev` | yes | the build's version and git revision (`internal/build`) |
| `err` | no | an error, as `{type, message, stack}`; see below |
| `trace_id`, `span_id` | no | the request's trace, when logged inside one (`tracing/README.md`) |

Records go to stderr. The test that pins this shape is `TestRecordShape` in
`format_test.go`.

### Errors

An error value logged under the key `err` (`log.ErrorKey`) is serialised the way
pino's error serialiser does it:

- `type` — the error's dynamic Go type, e.g. `*fs.PathError`;
- `message` — its `Error()` text;
- `stack` — the stack of the **log site**, at ERROR and above only. Go errors do
  not carry a stack of their own, and capturing one costs the same on every
  record, so records below ERROR go without.

An ERROR record with no error value carries no stack at all.

## Building a logger

```go
slog.SetDefault(log.New(os.Stderr, lvl, build.Version, build.GitRevision))
```

`log.New` is the one place the record's fields are produced. `cmd/shigola` and
`cmd/shigola_lambda` both call it; tests reach it through `internal/fakelog`.
`log.ParseLogLevel` turns the `--log-level` flag into a level.

## Logging

Anything that can use slog directly should. The package-level helpers exist for
the call sites inherited from tegola, and log through `slog.Default()`:

| Helpers | Message |
|:---|:---|
| `Errorf`, `Warnf`, `Infof`, `Debugf` | `fmt.Sprintf(format, args...)` |
| `ErrorfContext`, `WarnfContext`, `InfofContext`, `DebugfContext` | the same, with the request's trace ids |
| `Error`, `Warn`, `Info`, `Debug` | the operands as `fmt.Println` joins them |

All of them attach the first non-nil `error` among their arguments under `err`,
so `log.Errorf("tier get: %v", err)` produces both a readable message and a
structured error. A disabled level returns before formatting anything.

`fmt.Println` puts a space between every pair of operands, so a message passed to
`Error`, `Warn`, `Info` or `Debug` should not end in one: `log.Info("zoom list:", zooms)`.
