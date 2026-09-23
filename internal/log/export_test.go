package log_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/MapColonies/shigola/internal/fakelog"
	"github.com/MapColonies/shigola/internal/log"
)

// exportRecorder is an slog.Handler standing in for an export — in production
// the OTLP bridge — that keeps what it was handed, flattened to key paths so a
// test can ask for "err.message" without walking groups.
type exportRecorder struct {
	mu      *sync.Mutex
	records *[]exported
	// prefix and bound are what WithGroup and WithAttrs accumulated on this
	// derived handler.
	prefix string
	bound  map[string]any
	fail   error
}

type exported struct {
	ctx   context.Context
	level slog.Level
	msg   string
	attrs map[string]any
}

func newExportRecorder() *exportRecorder {
	return &exportRecorder{mu: &sync.Mutex{}, records: &[]exported{}, bound: map[string]any{}}
}

func (h *exportRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *exportRecorder) Handle(ctx context.Context, r slog.Record) error {
	attrs := map[string]any{}
	for k, v := range h.bound {
		attrs[k] = v
	}
	r.Attrs(func(a slog.Attr) bool {
		flatten(attrs, h.prefix, a)
		return true
	})

	h.mu.Lock()
	*h.records = append(*h.records, exported{ctx: ctx, level: r.Level, msg: r.Message, attrs: attrs})
	h.mu.Unlock()

	return h.fail
}

func (h *exportRecorder) WithAttrs(as []slog.Attr) slog.Handler {
	bound := map[string]any{}
	for k, v := range h.bound {
		bound[k] = v
	}
	for _, a := range as {
		flatten(bound, h.prefix, a)
	}

	derived := *h
	derived.bound = bound

	return &derived
}

func (h *exportRecorder) WithGroup(name string) slog.Handler {
	derived := *h
	derived.prefix = h.prefix + name + "."

	return &derived
}

func flatten(into map[string]any, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		for _, g := range a.Value.Group() {
			flatten(into, prefix+a.Key+".", g)
		}
		return
	}
	into[prefix+a.Key] = a.Value.Any()
}

func (h *exportRecorder) all() []exported {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]exported(nil), *h.records...)
}

// TestExportGetsTheRecordWithoutStderrsExtras is the division of labour
// between the two outputs. stderr's record has to carry the process identity
// and the trace ids as fields, because a line of JSON has nowhere else to put
// them; an OTLP log record does — the resource and the record's own trace
// context — so as attributes they would only be duplicates. Worse than noise
// in Loki: its OTLP intake stores the native trace id as trace_id already.
func TestExportGetsTheRecordWithoutStderrsExtras(t *testing.T) {
	var buf bytes.Buffer
	export := newExportRecorder()
	logger := log.New(&buf, slog.LevelInfo, "v1.2.3", "cafe123", export)

	ctx := fakelog.TracedContext(true)
	logger.ErrorContext(ctx, "tier get failed", "tier", "redis", log.ErrorKey, errors.New("refused"))

	got := export.all()
	if len(got) != 1 {
		t.Fatalf("export got %d records, want 1", len(got))
	}
	rec := got[0]

	if rec.msg != "tier get failed" || rec.level != slog.LevelError {
		t.Errorf("export got %v %q, want ERROR %q", rec.level, rec.msg, "tier get failed")
	}
	if rec.attrs["tier"] != "redis" {
		t.Errorf("tier = %v, want redis", rec.attrs["tier"])
	}
	// The same err shape as stderr's, so a query written against one reads
	// the other.
	if rec.attrs["err.message"] != "refused" || rec.attrs["err.type"] != "*errors.errorString" {
		t.Errorf("err = %v / %v, want the serialised error", rec.attrs["err.type"], rec.attrs["err.message"])
	}
	if _, ok := rec.attrs["err.stack"]; !ok {
		t.Error("err.stack absent on an ERROR record")
	}
	// The context itself is handed on, which is what the OTLP bridge reads
	// the trace and span from.
	if rec.ctx != ctx {
		t.Error("export was not handed the caller's context")
	}

	for _, key := range []string{"pid", "hostname", "version", "rev", log.TraceIDKey, log.SpanIDKey} {
		if v, ok := rec.attrs[key]; ok {
			t.Errorf("%v = %v on the export, want it left to the resource and trace context", key, v)
		}
	}

	// stderr is unchanged by there being an export.
	record := decode(t, &buf)
	for _, key := range []string{"pid", "hostname", "version", "rev", log.TraceIDKey, log.SpanIDKey} {
		if _, ok := record[key]; !ok {
			t.Errorf("stderr record lost %v", key)
		}
	}
}

// TestExportFollowsTheLevel — one --log-level for both outputs. Two would be
// a second knob nobody asked for, and an export quietly shipping DEBUG lines a
// deployment turned off would be a bill nobody expected.
func TestExportFollowsTheLevel(t *testing.T) {
	var buf bytes.Buffer
	export := newExportRecorder()
	logger := log.New(&buf, slog.LevelWarn, "", "", export)

	logger.Info("dropped")
	logger.Warn("kept")

	got := export.all()
	if len(got) != 1 || got[0].msg != "kept" {
		t.Errorf("export got %v, want only the WARN record", got)
	}
}

// TestExportSeesWhatCallersBind — attributes and groups a caller binds with
// With and WithGroup belong to the record, so they reach every output.
func TestExportSeesWhatCallersBind(t *testing.T) {
	var buf bytes.Buffer
	export := newExportRecorder()
	logger := log.New(&buf, slog.LevelInfo, "", "", export)

	logger.With("map", "osm").WithGroup("tile").Info("served", "z", 3)

	got := export.all()
	if len(got) != 1 {
		t.Fatalf("export got %d records, want 1", len(got))
	}
	if got[0].attrs["map"] != "osm" {
		t.Errorf("map = %v, want osm", got[0].attrs["map"])
	}
	if got[0].attrs["tile.z"] != int64(3) {
		t.Errorf("tile.z = %#v, want 3", got[0].attrs["tile.z"])
	}
}

// TestAFailingExportDoesNotCostStderr — a collector that is down must not
// take the local log with it, which is the reason stderr is kept at all.
func TestAFailingExportDoesNotCostStderr(t *testing.T) {
	var buf bytes.Buffer
	export := newExportRecorder()
	export.fail = errors.New("collector down")
	logger := log.New(&buf, slog.LevelInfo, "", "", export)

	logger.Info("still here")

	if got := decode(t, &buf)["msg"]; got != "still here" {
		t.Errorf("stderr msg = %v, want still here", got)
	}
}
