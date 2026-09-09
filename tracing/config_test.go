package tracing

import (
	"testing"
	"time"

	"github.com/MapColonies/shigola/internal/env"
)

func TestConfigValidate(t *testing.T) {
	type tcase struct {
		config  Config
		invalid bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			err := tc.config.Validate()

			if tc.invalid && err == nil {
				t.Error("Validate() = nil, expected an error")
			}
			if !tc.invalid && err != nil {
				t.Errorf("Validate() = %v, expected nil", err)
			}
		}
	}

	ratio := func(f float64) *env.Float { v := env.Float(f); return &v }

	tests := map[string]tcase{
		// The case every config in the tree today falls into: no [tracing]
		// section at all.
		"absent section": {config: Config{}},
		"default exporter": {
			config: Config{Enabled: true},
		},
		"grpc exporter": {
			config: Config{Enabled: true, Exporter: ExporterOTLPGRPC},
		},
		"http exporter": {
			config: Config{Enabled: true, Exporter: ExporterOTLPHTTP},
		},
		"unknown exporter": {
			config:  Config{Enabled: true, Exporter: "jaeger"},
			invalid: true,
		},
		// Checked while disabled too, which is the point: a typo waiting in a
		// switched-off section is found now rather than on the day someone
		// switches it on.
		"unknown exporter while disabled": {
			config:  Config{Exporter: "jaeger"},
			invalid: true,
		},
		"ratio at zero":               {config: Config{Enabled: true, SampleRatio: ratio(0)}},
		"ratio at one":                {config: Config{Enabled: true, SampleRatio: ratio(1)}},
		"ratio below zero":            {config: Config{Enabled: true, SampleRatio: ratio(-0.1)}, invalid: true},
		"ratio above one":             {config: Config{Enabled: true, SampleRatio: ratio(1.5)}, invalid: true},
		"negative timeout":            {config: Config{Enabled: true, TimeoutMS: -1}, invalid: true},
		"zero timeout is the default": {config: Config{Enabled: true, TimeoutMS: 0}},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestConfigDefaults pins what an enabled section that says nothing else
// resolves to — including the one value a reader is most likely to get wrong.
func TestConfigDefaults(t *testing.T) {
	cfg := Config{Enabled: true}

	if got := cfg.ExporterName(); got != ExporterOTLPGRPC {
		t.Errorf("ExporterName() = %v, want %v", got, ExporterOTLPGRPC)
	}
	if got := cfg.Service(); got != DefaultServiceName {
		t.Errorf("Service() = %v, want %v", got, DefaultServiceName)
	}
	if got := cfg.Ratio(); got != DefaultSampleRatio {
		t.Errorf("Ratio() = %v, want %v", got, DefaultSampleRatio)
	}
	if got := cfg.Timeout(); got != DefaultTimeout {
		t.Errorf("Timeout() = %v, want %v", got, DefaultTimeout)
	}
	if got := cfg.HeaderMap(); got != nil {
		t.Errorf("HeaderMap() = %v, want nil", got)
	}
}

// TestConfigZeroSampleRatioIsNotTheDefault is why SampleRatio is a pointer.
//
// "sample_ratio = 0" is a real instruction — record nothing this service roots,
// but still record a trace an upstream service already decided to sample — and
// a plain float64 could not tell it from an omitted key, so it would silently
// become 1%.
func TestConfigZeroSampleRatioIsNotTheDefault(t *testing.T) {
	zero := env.Float(0)
	cfg := Config{Enabled: true, SampleRatio: &zero}

	if got := cfg.Ratio(); got != 0 {
		t.Errorf("Ratio() = %v, want 0", got)
	}
}

func TestConfigOverrides(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		Exporter:    ExporterOTLPHTTP,
		ServiceName: "shigola-qa",
		TimeoutMS:   250,
		Headers:     env.Dict{"x-scope-orgid": "tenant-a"},
	}

	if got := cfg.ExporterName(); got != ExporterOTLPHTTP {
		t.Errorf("ExporterName() = %v, want %v", got, ExporterOTLPHTTP)
	}
	if got := cfg.Service(); got != "shigola-qa" {
		t.Errorf("Service() = %v, want shigola-qa", got)
	}
	if got, want := cfg.Timeout(), 250*time.Millisecond; got != want {
		t.Errorf("Timeout() = %v, want %v", got, want)
	}
	if got := cfg.HeaderMap()["x-scope-orgid"]; got != "tenant-a" {
		t.Errorf("HeaderMap()[x-scope-orgid] = %q, want tenant-a", got)
	}
}
