package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/tracing"
)

// The minimum a config needs to validate, so these cases are about the
// [tracing] section and nothing else.
const tracingConfigTemplate = `
[[providers]]
name = "provider1"
type = "mvt_test"

[[maps]]
name = "osm"
	[[maps.layers]]
	provider_layer = "provider1.water"
	min_zoom = 0
	max_zoom = 14
%s
`

func parseTracing(t *testing.T, section string) config.Config {
	t.Helper()

	conf, err := config.Parse(strings.NewReader(strings.Replace(tracingConfigTemplate, "%s", section, 1)), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	return conf
}

// TestParseTracingAbsent is the default every config in the tree today gets:
// no [tracing] section, so tracing is off and nothing else about the file
// changes meaning.
func TestParseTracingAbsent(t *testing.T) {
	conf := parseTracing(t, "")

	if bool(conf.Tracing.Enabled) {
		t.Error("tracing is enabled by a config that never mentions it")
	}
	if err := conf.Validate(); err != nil {
		t.Errorf("Validate() = %v on a config with no [tracing] section", err)
	}
}

func TestParseTracingSection(t *testing.T) {
	conf := parseTracing(t, `
[tracing]
enabled = true
exporter = "otlp_http"
endpoint = "tempo.observability:4318"
insecure = true
sample_ratio = 0.25
service_name = "shigola-qa"
timeout_ms = 3000
	[tracing.headers]
	x-scope-orgid = "tenant-a"
`)

	if err := conf.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	tr := conf.Tracing

	if !bool(tr.Enabled) {
		t.Error("enabled = false")
	}
	if got := tr.ExporterName(); got != tracing.ExporterOTLPHTTP {
		t.Errorf("ExporterName() = %v, want %v", got, tracing.ExporterOTLPHTTP)
	}
	if got := string(tr.Endpoint); got != "tempo.observability:4318" {
		t.Errorf("endpoint = %q", got)
	}
	if !bool(tr.Insecure) {
		t.Error("insecure = false")
	}
	if got := tr.Ratio(); got != 0.25 {
		t.Errorf("Ratio() = %v, want 0.25", got)
	}
	if got := tr.Service(); got != "shigola-qa" {
		t.Errorf("Service() = %q", got)
	}
	if got, want := tr.Timeout(), 3*time.Second; got != want {
		t.Errorf("Timeout() = %v, want %v", got, want)
	}
	if got := tr.HeaderMap()["x-scope-orgid"]; got != "tenant-a" {
		t.Errorf("HeaderMap()[x-scope-orgid] = %q, want tenant-a", got)
	}
}

// TestValidateRejectsABadTracingSection is why Validate reaches into the
// section rather than leaving it to whoever builds the backend: an unusable
// value is a startup error at the same point every other config mistake is,
// and it is one whether or not tracing is switched on.
func TestValidateRejectsABadTracingSection(t *testing.T) {
	type tcase struct {
		section string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			conf := parseTracing(t, tc.section)

			if err := conf.Validate(); err == nil {
				t.Error("Validate() = nil, expected an error")
			}
		}
	}

	tests := map[string]tcase{
		"unknown exporter": {section: "\n[tracing]\nenabled = true\nexporter = \"jaeger\"\n"},
		// Rejected while disabled too: a typo waiting in a switched-off
		// section should not first be discovered on the day it is switched on.
		"unknown exporter while disabled": {section: "\n[tracing]\nexporter = \"zipkin\"\n"},
		"sample ratio above one":          {section: "\n[tracing]\nenabled = true\nsample_ratio = 2.0\n"},
		"negative timeout":                {section: "\n[tracing]\nenabled = true\ntimeout_ms = -5\n"},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
