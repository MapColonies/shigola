package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tracing"
)

// failingChain builds hot → durable where hot fails every read, so a request
// through it produces the one log line a broken tier produces in production.
func failingChain(t *testing.T, hotType, durableType string, readErr error) cache.Interface {
	t.Helper()

	hot := faketier.New(hotType)
	hot.FailOn(faketier.OpGet, readErr)

	for cacheType, tier := range map[string]*faketier.Tier{
		hotType:     hot,
		durableType: faketier.New(durableType),
	} {
		c := tier
		if err := cache.Register(cacheType, func(dict.Dicter) (cache.Interface, error) { return c, nil }); err != nil {
			t.Fatalf("register %v: %v", cacheType, err)
		}
	}

	c, err := cache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			{"type": hotType, "name": hotType},
			{"type": durableType, "name": durableType},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}

	return c
}

// captureLogs redirects the default logger — which is what the internal/log
// helpers write to — through the handler under test, and returns the buffer.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(log.NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return &buf
}

// findRecord returns the one captured record whose message contains want.
func findRecord(t *testing.T, buf *bytes.Buffer, want string) map[string]any {
	t.Helper()

	var found []map[string]any
	var messages []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}

		msg, _ := record["msg"].(string)
		messages = append(messages, msg)
		if strings.Contains(msg, want) {
			found = append(found, record)
		}
	}

	if len(found) != 1 {
		t.Fatalf("%d records containing %q, want 1; captured %v", len(found), want, messages)
	}

	return found[0]
}

// TestRequestLogsCarryTheRequestsTraceIDs is the acceptance criterion end to
// end: a log line emitted while serving a traced request names that request's
// trace, so Tempo and Loki reach each other.
//
// Worth having as well as the handler's own unit tests, because what it checks
// beyond their sum is the plumbing: that the span the HTTP middleware starts
// reaches a context four decorators down in the cache chain, and that the ids
// on the record are the same ones the exporter recorded rather than merely
// well-formed. A tier read failure is the line to prove it on — per the cache
// invariant it is the only evidence a tier is broken, so it is the line an
// operator most needs to reach from a trace.
func TestRequestLogsCarryTheRequestsTraceIDs(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	tracer, exporter := faketracer.New(t)

	a := newTestMapWithLayers(testLayer2)
	a.SetCache(failingChain(t, "corrhot", "corrdurable", errors.New("tier is down")))
	a.SetTracing(tracer)

	buf := captureLogs(t)

	w, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// The read failure is a miss, never an error: the tile is still served.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	record := findRecord(t, buf, "cache/multi: tier (corrhot) get:")

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("the request recorded no spans at all")
	}

	want := spans[0].SpanContext.TraceID().String()
	if got := record[log.TraceIDKey]; got != want {
		t.Errorf("%v = %v, want the request's trace %v", log.TraceIDKey, got, want)
	}

	// The record belongs to the span whose context the chain was called with,
	// not merely to the right trace: the whole-cache read is what wraps the
	// chain, so that is the span the line hangs off in Tempo.
	cacheGet := faketracer.SpanNamed(t, exporter, tracing.SpanCacheGet)
	if got, want := record[log.SpanIDKey], cacheGet.SpanContext.SpanID().String(); got != want {
		t.Errorf("%v = %v, want the %v span %v", log.SpanIDKey, got, tracing.SpanCacheGet, want)
	}
}

// TestRequestLogsWithoutTracingCarryNoCorrelation is the default: with no
// tracing backend the same failure logs the same message and adds no fields.
func TestRequestLogsWithoutTracingCarryNoCorrelation(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer2)
	a.SetCache(failingChain(t, "uncorrhot", "uncorrdurable", errors.New("tier is down")))

	buf := captureLogs(t)

	if _, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil); err != nil {
		t.Fatalf("request: %v", err)
	}

	record := findRecord(t, buf, "cache/multi: tier (uncorrhot) get:")
	for _, key := range []string{log.TraceIDKey, log.SpanIDKey} {
		if got, ok := record[key]; ok {
			t.Errorf("%v present as %v, want the key absent", key, got)
		}
	}
}
