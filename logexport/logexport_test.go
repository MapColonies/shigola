package logexport_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/fakelog"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/logexport"
)

// TestNewDisabledBuildsNothing — off by default. A disabled section returns
// no export at all, so nothing is dialled and log.New gets no extra output.
func TestNewDisabledBuildsNothing(t *testing.T) {
	type tcase struct {
		config logexport.Config
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			export, err := logexport.New(context.Background(), tc.config)
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			if export != nil {
				t.Errorf("New() = %v for a disabled config, want nil", export)
			}
		}
	}

	tests := map[string]tcase{
		"absent section": {config: logexport.Config{}},
		"configured but disabled": {
			config: logexport.Config{Exporter: logexport.ExporterOTLPHTTP, Endpoint: "loki:4318"},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestValidate — a section that cannot be honoured is a startup error, named
// by its section, whether or not it is enabled.
func TestValidate(t *testing.T) {
	type tcase struct {
		config logexport.Config
		// want is a substring of the error, or "" for none.
		want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			err := tc.config.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"empty":            {config: logexport.Config{}},
		"host and port":    {config: logexport.Config{Enabled: true, Endpoint: "loki:4317", Insecure: true}},
		"full URL":         {config: logexport.Config{Endpoint: "https://collector.example/v1/logs"}},
		"unknown exporter": {config: logexport.Config{Exporter: "syslog"}, want: "logging.otlp: exporter (syslog)"},
		// The shared endpoint check, reporting this section rather than
		// tracing's.
		"a path with no scheme": {config: logexport.Config{Endpoint: "loki:4318/v1/logs"}, want: "logging.otlp: endpoint"},
		"https and insecure": {
			config: logexport.Config{Endpoint: "https://collector.example", Insecure: true},
			want:   "logging.otlp: endpoint",
		},
		"negative timeout": {config: logexport.Config{TimeoutMS: -1}, want: "logging.otlp: timeout_ms"},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// collector is an OTLP/HTTP logs receiver: it decodes what the exporter sends
// so a test can assert on the records as a backend would store them.
type collector struct {
	mu       sync.Mutex
	path     string
	requests []*collogs.ExportLogsServiceRequest
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := &collogs.ExportLogsServiceRequest{}
	if err := proto.Unmarshal(body, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	c.mu.Lock()
	c.path = r.URL.Path
	c.requests = append(c.requests, req)
	c.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (c *collector) records() ([]*logspb.LogRecord, map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		records  []*logspb.LogRecord
		resource = map[string]string{}
	)
	for _, req := range c.requests {
		for _, rl := range req.GetResourceLogs() {
			for _, kv := range rl.GetResource().GetAttributes() {
				resource[kv.GetKey()] = kv.GetValue().GetStringValue()
			}
			for _, sl := range rl.GetScopeLogs() {
				records = append(records, sl.GetLogRecords()...)
			}
		}
	}

	return records, resource
}

func attr(r *logspb.LogRecord, key string) (string, bool) {
	for _, kv := range r.GetAttributes() {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue(), true
		}
	}

	return "", false
}

// TestRecordsReachTheCollector is the end to end: a record logged through the
// logger the binaries build arrives at an OTLP collector with its severity,
// its message, its attributes, its err, and its trace — and with the process
// described by the resource rather than repeated on the record.
func TestRecordsReachTheCollector(t *testing.T) {
	col := &collector{}
	srv := httptest.NewServer(col)
	t.Cleanup(srv.Close)

	export, err := logexport.New(context.Background(), logexport.Config{
		Enabled:  true,
		Exporter: logexport.ExporterOTLPHTTP,
		// A full URL, the shape that used to fail silently for traces.
		Endpoint:    env.String(srv.URL + "/v1/logs"),
		ServiceName: "shigola-test",
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	var stderr bytes.Buffer
	logger := log.New(&stderr, slog.LevelInfo, "v1.2.3", "cafe123", export.Handler())
	logger.ErrorContext(fakelog.TracedContext(true), "tier get failed", "tier", "redis", log.ErrorKey, errors.New("refused"))
	logger.Debug("below the level")

	// Shutdown flushes the batch processor synchronously.
	if err := export.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v", err)
	}

	records, resource := col.records()
	if col.path != "/v1/logs" {
		t.Errorf("collector saw path %q, want /v1/logs", col.path)
	}
	if len(records) != 1 {
		t.Fatalf("collector got %d records, want 1", len(records))
	}
	rec := records[0]

	if got := rec.GetBody().GetStringValue(); got != "tier get failed" {
		t.Errorf("body = %q, want the message", got)
	}
	if rec.GetSeverityNumber() != logspb.SeverityNumber_SEVERITY_NUMBER_ERROR {
		t.Errorf("severity = %v, want ERROR", rec.GetSeverityNumber())
	}
	if got, _ := attr(rec, "tier"); got != "redis" {
		t.Errorf("tier = %q, want redis", got)
	}
	if got := hex.EncodeToString(rec.GetTraceId()); got != fakelog.TraceIDHex {
		t.Errorf("trace id = %v, want %v", got, fakelog.TraceIDHex)
	}
	if got := hex.EncodeToString(rec.GetSpanId()); got != fakelog.SpanIDHex {
		t.Errorf("span id = %v, want %v", got, fakelog.SpanIDHex)
	}

	found := false
	for _, kv := range rec.GetAttributes() {
		if kv.GetKey() != log.ErrorKey {
			continue
		}
		found = true
		fields := map[string]string{}
		for _, f := range kv.GetValue().GetKvlistValue().GetValues() {
			fields[f.GetKey()] = f.GetValue().GetStringValue()
		}
		if fields["message"] != "refused" || fields["type"] != "*errors.errorString" || fields["stack"] == "" {
			t.Errorf("err = %v, want type, message and stack", fields)
		}
	}
	if !found {
		t.Error("err attribute absent")
	}

	for _, key := range []string{"pid", "hostname", "version", "rev", log.TraceIDKey, log.SpanIDKey} {
		if _, ok := attr(rec, key); ok {
			t.Errorf("%v on the record, want it left to the resource and trace context", key)
		}
	}
	if resource["service.name"] != "shigola-test" {
		t.Errorf("resource service.name = %q, want shigola-test", resource["service.name"])
	}

	// stderr kept its own copy.
	if !strings.Contains(stderr.String(), `"msg":"tier get failed"`) {
		t.Errorf("stderr lost the record: %s", stderr.String())
	}
}

// TestFlushIsSafeWithoutAnExport — the shutdown paths call it unconditionally.
func TestFlushIsSafeWithoutAnExport(t *testing.T) {
	logexport.Flush(nil)
}
