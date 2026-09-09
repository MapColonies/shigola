// Package fakelog captures structured log records in tests, plus the trace
// context and the assertions that go with them.
//
// It exists because three packages need to assert on what a log record carries
// once a request is traced — internal/log for the handler itself, server for a
// real request, provider/postgis for pgx's statement logger — and doing it by
// hand is the same block in each: a buffer, the handler under test around it,
// slog's default swapped and restored, then a line of JSON to unmarshal and
// search. Written three times it also invited three spellings of the same
// fixed trace id.
//
// It lives under internal/ so those tests can share it without it becoming
// public API, alongside faketier and faketracer, which exist for the same
// reason on the cache and tracing sides.
//
// The recorder deliberately wraps the real internal/log.Handler rather than a
// plain JSON handler: what these tests are about is what that handler adds, so
// a fake in its place would assert nothing.
package fakelog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/log"
)

// The ids are fixed rather than generated: an assertion that quotes the exact
// hex it expects says more on failure than one comparing two values the test
// itself just made up. They are the W3C trace-context specification's own
// examples, so they are recognisable as synthetic.
var (
	TraceID = trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
	SpanID  = trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
)

// TraceIDHex and SpanIDHex are TraceID and SpanID as a record carries them.
const (
	TraceIDHex = "4bf92f3577b34da6a3ce929d0e0e4736"
	SpanIDHex  = "00f067aa0ba902b7"
)

// TracedContext returns a context carrying TraceID and SpanID.
//
// Built without an SDK: the handler reads the span context off the context and
// nothing else, so a real tracer would only add moving parts. Use faketracer
// where the spans themselves are the subject.
func TracedContext(sampled bool) context.Context {
	return ContextWith(TraceID, SpanID, sampled)
}

// ContextWith is TracedContext for ids a test chooses itself, which is what an
// invalid span context needs — the zero trace id is what an uninitialised or
// stripped one looks like.
func ContextWith(traceID trace.TraceID, spanID trace.SpanID, sampled bool) context.Context {
	cfg := trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}
	if sampled {
		cfg.TraceFlags = trace.FlagsSampled
	}

	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(cfg))
}

// Recorder holds the records written through it, as JSON.
type Recorder struct {
	buf bytes.Buffer
}

// New returns a recorder and the logger writing into it, leaving slog's default
// alone — for a test that holds its own logger.
//
// Every level is enabled, so a test asserting that something is *not* logged is
// testing the code rather than the handler options.
func New() (*slog.Logger, *Recorder) {
	rec := &Recorder{}
	handler := log.NewHandler(slog.NewJSONHandler(&rec.buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return slog.New(handler), rec
}

// Default installs New's logger as slog's default for the rest of the test,
// restoring the previous one afterwards — for the call sites that log through
// the package-level helpers, which is all of them on the request path.
//
// Process-wide while it is installed, so a test using it must not run in
// parallel with one that logs.
func Default(t *testing.T) *Recorder {
	t.Helper()

	logger, rec := New()
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	return rec
}

// Raw returns everything written, for a failure message.
func (r *Recorder) Raw() string { return r.buf.String() }

// Records returns every record written, decoded.
func (r *Recorder) Records(t *testing.T) []map[string]any {
	t.Helper()

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.buf.String()), "\n") {
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}

		records = append(records, record)
	}

	return records
}

// One returns the single record written, failing the test if there is not
// exactly one.
func (r *Recorder) One(t *testing.T) map[string]any {
	t.Helper()

	records := r.Records(t)
	if len(records) != 1 {
		t.Fatalf("%d records written, want 1: %v", len(records), Messages(records))
	}

	return records[0]
}

// Containing returns the single record whose message contains want, failing the
// test if there is not exactly one. Searching by substring rather than by
// equality because the messages these tests look for are formatted, and the
// interesting half is usually an error the test did not write.
func (r *Recorder) Containing(t *testing.T, want string) map[string]any {
	t.Helper()

	records := r.Records(t)

	var found []map[string]any
	for _, record := range records {
		if msg, _ := record["msg"].(string); strings.Contains(msg, want) {
			found = append(found, record)
		}
	}

	if len(found) != 1 {
		t.Fatalf("%d records containing %q, want 1; wrote %v", len(found), want, Messages(records))
	}

	return found[0]
}

// Empty reports whether nothing was written at all, which is what a dropped
// level looks like.
func (r *Recorder) Empty() bool { return r.buf.Len() == 0 }

// Messages renders records as their messages, which is usually enough to say
// what a failing assertion actually saw.
func Messages(records []map[string]any) []string {
	out := make([]string, len(records))
	for i := range records {
		out[i], _ = records[i]["msg"].(string)
	}

	return out
}

// Correlation reads the trace and span ids off a record. Absent keys come back
// as "", which is what a record outside a trace must look like — the handler
// adds no empty fields, so a test asserting absence checks for "" here and the
// key's absence in the map.
func Correlation(record map[string]any) (traceID, spanID string) {
	traceID, _ = record[log.TraceIDKey].(string)
	spanID, _ = record[log.SpanIDKey].(string)

	return traceID, spanID
}

// AssertCorrelation checks that a record names the given trace and span. Empty
// arguments assert the keys are absent entirely rather than present and empty,
// which is the requirement for a record written outside any trace.
func AssertCorrelation(t *testing.T, record map[string]any, wantTrace, wantSpan string) {
	t.Helper()

	for key, want := range map[string]string{
		log.TraceIDKey: wantTrace,
		log.SpanIDKey:  wantSpan,
	} {
		got, present := record[key]
		switch {
		case want == "" && present:
			t.Errorf("%v: present as %v, want the key absent", key, got)
		case want != "" && !present:
			t.Errorf("%v: absent, want %v", key, want)
		case want != "" && got != want:
			t.Errorf("%v: %v, want %v", key, got, want)
		}
	}
}
