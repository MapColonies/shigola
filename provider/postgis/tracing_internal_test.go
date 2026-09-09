package postgis

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/tracing"
)

// These need no database. startQuerySpan reads only the provider's static
// attributes and the span in the context, which is the point of taking the
// tracer from the context rather than being wired one — so the span's shape is
// testable without RUN_POSTGIS_TESTS, and the gated tests are left to cover
// what actually needs a server.

func tracedProvider() Provider {
	return Provider{
		name:    "mvt_test",
		dbAttrs: tracing.DBServerAttrs("db.internal", 5432, "shigola"),
	}
}

// TestStartQuerySpanDescribesTheQuery is the acceptance criterion: the SQL, the
// server and the duration of the database's own work.
//
// Duration is the span's, so it is asserted by the span existing and being
// ended around the round trip — see MVTForLayers. What is checked here is that
// it hangs off the provider span and carries what a reader needs to identify
// the statement and the server it went to.
func TestStartQuerySpanDescribesTheQuery(t *testing.T) {
	backend, exporter := faketracer.New(t)

	const sql = `SELECT (SELECT ST_AsMVT(q,'water',4096,'geom',NULL) AS data FROM (SELECT * FROM water WHERE geom && $1) AS q) AS data`

	// A parent, because that is what the atlas provides in production and what
	// the child has to attach to.
	ctx, parent := backend.Tracer().Start(context.Background(), tracing.SpanProviderQuery)

	_, span := tracedProvider().startQuerySpan(ctx, sql)
	span.End()
	parent.End()

	query := faketracer.SpanNamed(t, exporter, tracing.SpanPostgisQuery)

	if query.Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Error("the query span is not a child of the provider span")
	}
	if query.SpanKind != trace.SpanKindClient {
		t.Errorf("span kind = %v, want client — Tempo renders a database call by its kind", query.SpanKind)
	}

	if got := faketracer.StringAttr(query, semconv.DBQueryTextKey); got != sql {
		t.Errorf("db.query.text = %q, want the statement", got)
	}
	if got := faketracer.StringAttr(query, semconv.ServerAddressKey); got != "db.internal" {
		t.Errorf("server.address = %q, want db.internal", got)
	}
	if got := faketracer.Int64Attr(query, semconv.ServerPortKey); got != 5432 {
		t.Errorf("server.port = %v, want 5432", got)
	}
	if got := faketracer.StringAttr(query, semconv.DBNamespaceKey); got != "shigola" {
		t.Errorf("db.namespace = %q, want shigola", got)
	}
	if got := faketracer.StringAttr(query, semconv.DBSystemNameKey); got != "postgresql" {
		t.Errorf("db.system.name = %q, want postgresql", got)
	}
}

// TestStartQuerySpanCarriesNoCredentials is the security half of the same
// criterion. A pgx ConnConfig carries the user and the password alongside the
// host, and DBServerAttrs takes only three of its fields for exactly this
// reason — so this asserts the omission rather than trusting it.
func TestStartQuerySpanCarriesNoCredentials(t *testing.T) {
	backend, exporter := faketracer.New(t)

	ctx, parent := backend.Tracer().Start(context.Background(), tracing.SpanProviderQuery)
	_, span := tracedProvider().startQuerySpan(ctx, "SELECT 1")
	span.End()
	parent.End()

	query := faketracer.SpanNamed(t, exporter, tracing.SpanPostgisQuery)

	for _, kv := range query.Attributes {
		key := string(kv.Key)
		for _, forbidden := range []string{"user", "password", "credential"} {
			if strings.Contains(key, forbidden) {
				t.Errorf("span carries a %v attribute (%v)", forbidden, key)
			}
		}
	}
}

// TestStartQuerySpanTruncatesALongStatement covers the bound. A many-layered
// map produces a very large statement, and a span that silently ended
// mid-query would read as a query that ended there.
func TestStartQuerySpanTruncatesALongStatement(t *testing.T) {
	backend, exporter := faketracer.New(t)

	sql := "SELECT " + strings.Repeat("x", tracing.MaxQueryTextBytes)

	ctx, parent := backend.Tracer().Start(context.Background(), tracing.SpanProviderQuery)
	_, span := tracedProvider().startQuerySpan(ctx, sql)
	span.End()
	parent.End()

	query := faketracer.SpanNamed(t, exporter, tracing.SpanPostgisQuery)

	text := faketracer.StringAttr(query, semconv.DBQueryTextKey)
	if len(text) > tracing.MaxQueryTextBytes {
		t.Errorf("db.query.text is %d bytes, over the %d cap", len(text), tracing.MaxQueryTextBytes)
	}
	if !faketracer.BoolAttr(query, tracing.AttrQueryTruncated) {
		t.Error("a truncated statement does not say so")
	}
}

// TestStartQuerySpanOnAnUntracedContextBuildsNothing is the cost criterion.
//
// With no span in the context the tracer is a no-op, so the span does not
// record — and the statement attribute, which is the expensive one, is never
// built. This asserts the non-recording part; the attribute is guarded on it.
func TestStartQuerySpanOnAnUntracedContextBuildsNothing(t *testing.T) {
	_, exporter := faketracer.New(t)

	_, span := tracedProvider().startQuerySpan(context.Background(), "SELECT 1")
	span.End()

	if span.IsRecording() {
		t.Error("a query on an untraced context started a recording span")
	}
	if got := exporter.GetSpans(); len(got) != 0 {
		t.Errorf("recorded %d spans from an untraced context: %v", len(got), faketracer.Names(got))
	}
}

// TestStartQuerySpanRecordsAFailure keeps the query span consistent with the
// rest of the tree: an error is recorded, and the status is the caller's own
// fault only when the caller did not walk away.
func TestStartQuerySpanRecordsAFailure(t *testing.T) {
	backend, exporter := faketracer.New(t)

	ctx, parent := backend.Tracer().Start(context.Background(), tracing.SpanProviderQuery)

	queryCtx, span := tracedProvider().startQuerySpan(ctx, "SELECT 1")
	tracing.RecordError(queryCtx, span, errors.New("pq: relation does not exist"))
	span.End()
	parent.End()

	query := faketracer.SpanNamed(t, exporter, tracing.SpanPostgisQuery)

	if query.Status.Code != codes.Error {
		t.Errorf("span status = %v, want error", query.Status.Code)
	}
	if len(query.Events) == 0 {
		t.Error("the error was not recorded on the span")
	}
}
