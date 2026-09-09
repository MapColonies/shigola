package tracing

import (
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// MaxQueryTextBytes bounds the statement text put on a span.
//
// A tile query is one statement per layer, unioned, with the tile's tokens
// already substituted — so a many-layered map produces a large one, and the
// attribute is built on the request path. 8KiB is comfortably more than enough
// to read what a query does while keeping one span's worth of attributes to
// something a collector will accept: OTEL's SDK applies no value-length limit
// by default, so without a cap here a wide map could emit spans measured in
// hundreds of kilobytes.
//
// A truncated statement says so, via AttrQueryTruncated, rather than looking
// like a query that simply ends abruptly.
const MaxQueryTextBytes = 8 << 10

// QueryText bounds a statement for use as a span attribute, reporting whether
// it had to cut.
//
// Cuts on a UTF-8 boundary. SQL is overwhelmingly ASCII, but layer names,
// schema names and string literals in a WHERE clause need not be, and half a
// rune in an attribute is invalid UTF-8 that a collector may reject.
func QueryText(sql string) (text string, truncated bool) {
	if len(sql) <= MaxQueryTextBytes {
		return sql, false
	}

	cut := MaxQueryTextBytes
	for cut > 0 && !utf8.RuneStart(sql[cut]) {
		cut--
	}

	return sql[:cut], true
}

// DBServerAttrs describes a database server for a span, in the OpenTelemetry
// database conventions.
//
// Built here rather than at the call site so the convention is chosen once:
// Grafana and Tempo recognise these keys and render a database span specially,
// which a shigola-specific spelling would not get. It deliberately carries no
// user and no password — a connection string has both, and neither belongs in
// a trace.
func DBServerAttrs(host string, port int, database string) []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.DBSystemNamePostgreSQL,
		semconv.ServerAddress(host),
		semconv.ServerPort(port),
		semconv.DBNamespace(database),
	}
}
