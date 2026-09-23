package log_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MapColonies/shigola/internal/log"
)

// decode reads the single JSON record in buf. json.Number rather than float64,
// so a time that is not an integer fails the assertion instead of rounding
// into one.
func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	dec := json.NewDecoder(buf)
	dec.UseNumber()

	var record map[string]any
	if err := dec.Decode(&record); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if dec.More() {
		t.Fatalf("more than one record written")
	}

	return record
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}

// TestRecordShape is the MAPCO-11544 contract in executable form: the record
// every MapColonies service writes, so one collector, one dashboard and one set
// of queries work across all of them. The reference is js-logger's output on
// its default (non-OTLP) path — pino with a label level formatter.
func TestRecordShape(t *testing.T) {
	var buf bytes.Buffer
	before := time.Now().UnixMilli()
	log.New(&buf, slog.LevelDebug, "v1.2.3", "cafe123").Info("some message")
	after := time.Now().UnixMilli()

	record := decode(t, &buf)

	// The whole key set, not a subset: a field that appears here unasked for is
	// as much a change to the contract as one that goes missing.
	want := []string{"hostname", "level", "msg", "pid", "rev", "time", "version"}
	if got := keys(record); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", got, want)
	}

	if got := record["level"]; got != "info" {
		t.Errorf("level = %v, want info", got)
	}
	if got := record["msg"]; got != "some message" {
		t.Errorf("msg = %v, want some message", got)
	}

	ms, err := record["time"].(json.Number).Int64()
	if err != nil {
		t.Fatalf("time = %v, want integer milliseconds: %v", record["time"], err)
	}
	if ms < before || ms > after {
		t.Errorf("time = %v, want within [%v, %v]", ms, before, after)
	}

	hostname, _ := os.Hostname()
	if got := record["hostname"]; got != hostname {
		t.Errorf("hostname = %v, want %v", got, hostname)
	}
	if got := record["pid"]; got != json.Number(strconv.Itoa(os.Getpid())) {
		t.Errorf("pid = %v, want %v", got, os.Getpid())
	}
	if got := record["version"]; got != "v1.2.3" {
		t.Errorf("version = %v, want v1.2.3", got)
	}
	if got := record["rev"]; got != "cafe123" {
		t.Errorf("rev = %v, want cafe123", got)
	}
}

// TestLevelLabels pins the label spelling to pino's: lowercase, which is what
// js-logger's level formatter writes. slog's own labels are uppercase.
func TestLevelLabels(t *testing.T) {
	type tcase struct {
		level slog.Level
		want  string
	}

	testCases := map[string]tcase{
		"debug": {level: slog.LevelDebug, want: "debug"},
		"info":  {level: slog.LevelInfo, want: "info"},
		"warn":  {level: slog.LevelWarn, want: "warn"},
		"error": {level: slog.LevelError, want: "error"},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			var buf bytes.Buffer
			log.New(&buf, slog.LevelDebug, "", "").Log(t.Context(), tc.level, "hello")

			if got := decode(t, &buf)["level"]; got != tc.want {
				t.Errorf("level = %v, want %v", got, tc.want)
			}
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}

// TestErrorSerialisation covers the err property: pino's err serialiser shape,
// with the stack inside it rather than as a sibling field, and only where there
// is an error to describe.
func TestErrorSerialisation(t *testing.T) {
	pathErr := &fs.PathError{Op: "open", Path: "/nope", Err: fs.ErrNotExist}

	type tcase struct {
		level slog.Level
		args  []any
		// wantErr is the err.type expected, or "" for no err property at all.
		wantErr   string
		wantStack bool
	}

	testCases := map[string]tcase{
		"an error at error level": {
			level:     slog.LevelError,
			args:      []any{log.ErrorKey, pathErr},
			wantErr:   "*fs.PathError",
			wantStack: true,
		},
		// A stack is the log site's, not the error's — Go errors carry none —
		// and capturing one costs the same on every record, so it is kept for
		// the records someone will actually read one on.
		"an error at warn level": {
			level:   slog.LevelWarn,
			args:    []any{log.ErrorKey, pathErr},
			wantErr: "*fs.PathError",
		},
		// The per-record debug.Stack() this replaced ran on every ERROR record
		// whether or not there was an error to explain.
		"an error record without an error": {
			level: slog.LevelError,
		},
		// Only an error value is serialised; an err attribute that is anything
		// else is the caller's own field and passes through untouched.
		"an err that is not an error": {
			level: slog.LevelError,
			args:  []any{log.ErrorKey, "just a string"},
		},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			var buf bytes.Buffer
			log.New(&buf, slog.LevelDebug, "", "").Log(t.Context(), tc.level, "it failed", tc.args...)

			record := decode(t, &buf)
			if _, ok := record["stack"]; ok {
				t.Errorf("stack: present at the top level, want it only inside err")
			}

			errObj, isObj := record[log.ErrorKey].(map[string]any)
			if tc.wantErr == "" {
				if isObj {
					t.Fatalf("err = %v, want no serialised error", errObj)
				}
				return
			}
			if !isObj {
				t.Fatalf("err = %#v, want an object", record[log.ErrorKey])
			}

			if got := errObj["type"]; got != tc.wantErr {
				t.Errorf("err.type = %v, want %v", got, tc.wantErr)
			}
			if got, want := errObj["message"], pathErr.Error(); got != want {
				t.Errorf("err.message = %v, want %v", got, want)
			}
			stack, hasStack := errObj["stack"].(string)
			if hasStack != tc.wantStack {
				t.Fatalf("err.stack present = %v, want %v", hasStack, tc.wantStack)
			}
			if hasStack && !strings.Contains(stack, "TestErrorSerialisation") {
				t.Errorf("err.stack does not reach the log site:\n%s", stack)
			}
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}
