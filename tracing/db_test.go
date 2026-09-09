package tracing_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/MapColonies/shigola/tracing"
)

func TestQueryText(t *testing.T) {
	type tcase struct {
		sql           string
		wantTruncated bool
		wantLen       int
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			text, truncated := tracing.QueryText(tc.sql)

			if truncated != tc.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
			if len(text) != tc.wantLen {
				t.Errorf("len(text) = %d, want %d", len(text), tc.wantLen)
			}
			if !utf8.ValidString(text) {
				t.Error("the returned text is not valid UTF-8, so a collector may reject the attribute")
			}
			if !truncated && text != tc.sql {
				t.Error("an untruncated statement was altered")
			}
		}
	}

	tests := map[string]tcase{
		"short":      {sql: "SELECT 1", wantLen: len("SELECT 1")},
		"empty":      {sql: "", wantLen: 0},
		"at the cap": {sql: strings.Repeat("x", tracing.MaxQueryTextBytes), wantLen: tracing.MaxQueryTextBytes},
		"over the cap": {
			sql:           strings.Repeat("x", tracing.MaxQueryTextBytes+1),
			wantTruncated: true,
			wantLen:       tracing.MaxQueryTextBytes,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestQueryTextCutsOnARuneBoundary is why the cut is not a plain slice.
//
// A layer name, a schema name or a string literal in a WHERE clause can be
// non-ASCII, and half a rune is invalid UTF-8 — which a collector may reject,
// taking the whole span with it.
func TestQueryTextCutsOnARuneBoundary(t *testing.T) {
	// Three-byte runes, so the cap lands mid-rune for two of every three
	// possible lengths.
	sql := strings.Repeat("א", tracing.MaxQueryTextBytes)

	text, truncated := tracing.QueryText(sql)

	if !truncated {
		t.Fatal("truncated = false for a statement well over the cap")
	}
	if !utf8.ValidString(text) {
		t.Error("the cut left invalid UTF-8")
	}
	if len(text) > tracing.MaxQueryTextBytes {
		t.Errorf("len(text) = %d, over the %d cap", len(text), tracing.MaxQueryTextBytes)
	}
}

// TestDBServerAttrsCarriesNoCredentials pins the omission. The fields come from
// a pgx ConnConfig, which also holds the user and the password.
func TestDBServerAttrsCarriesNoCredentials(t *testing.T) {
	attrs := tracing.DBServerAttrs("db.internal", 5432, "shigola")

	got := map[attribute.Key]attribute.Value{}
	for _, kv := range attrs {
		got[kv.Key] = kv.Value
	}

	for _, want := range []attribute.Key{
		semconv.DBSystemNameKey,
		semconv.ServerAddressKey,
		semconv.ServerPortKey,
		semconv.DBNamespaceKey,
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %v", want)
		}
	}

	if len(attrs) != 4 {
		t.Errorf("DBServerAttrs returned %d attributes, want exactly 4 — anything else is unaccounted for", len(attrs))
	}
}
