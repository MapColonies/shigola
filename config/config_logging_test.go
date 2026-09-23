package config_test

import (
	"strings"
	"testing"

	"github.com/MapColonies/shigola/logexport"
)

// Reuses tracingConfigTemplate: the minimum a config needs to validate, so
// these cases are about the [logging] section and nothing else.

// TestParseLoggingAbsent — every config in the tree today has no [logging]
// section, and must keep meaning stderr alone.
func TestParseLoggingAbsent(t *testing.T) {
	conf := parseTracing(t, "")

	if bool(conf.Logging.OTLP.Enabled) {
		t.Error("OTLP log export is enabled by a config that never mentions it")
	}
	if err := conf.Validate(); err != nil {
		t.Errorf("Validate() = %v on a config with no [logging] section", err)
	}
}

func TestParseLoggingOTLPSection(t *testing.T) {
	conf := parseTracing(t, `
[logging.otlp]
enabled = true
exporter = "otlp_http"
endpoint = "loki.observability:4318"
insecure = true
service_name = "shigola-eu"
timeout_ms = 5000

  [logging.otlp.headers]
  x-scope-orgid = "tenant-a"
`)

	if err := conf.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	got := conf.Logging.OTLP
	want := logexport.Config{
		Enabled:     true,
		Exporter:    logexport.ExporterOTLPHTTP,
		Endpoint:    "loki.observability:4318",
		Insecure:    true,
		ServiceName: "shigola-eu",
		TimeoutMS:   5000,
	}
	if got.Enabled != want.Enabled || got.Exporter != want.Exporter || got.Endpoint != want.Endpoint ||
		got.Insecure != want.Insecure || got.ServiceName != want.ServiceName || got.TimeoutMS != want.TimeoutMS {
		t.Errorf("[logging.otlp] = %+v, want %+v", got, want)
	}
	if got.Headers["x-scope-orgid"] != "tenant-a" {
		t.Errorf("headers = %v, want x-scope-orgid = tenant-a", got.Headers)
	}
}

// TestValidateRejectsABadLoggingSection — Config.Validate reaches the section,
// so a typo is a startup error rather than a silently absent export.
func TestValidateRejectsABadLoggingSection(t *testing.T) {
	conf := parseTracing(t, `
[logging.otlp]
exporter = "syslog"
`)

	err := conf.Validate()
	if err == nil || !strings.Contains(err.Error(), "logging.otlp") {
		t.Errorf("Validate() = %v, want a logging.otlp error", err)
	}
}
