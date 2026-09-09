package tracing_test

import (
	"testing"
	"time"

	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/tracing"
)

func TestConfigValidate(t *testing.T) {
	type tcase struct {
		config  tracing.Config
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
		"absent section": {config: tracing.Config{}},
		"default exporter": {
			config: tracing.Config{Enabled: true},
		},
		"grpc exporter": {
			config: tracing.Config{Enabled: true, Exporter: tracing.ExporterOTLPGRPC},
		},
		"http exporter": {
			config: tracing.Config{Enabled: true, Exporter: tracing.ExporterOTLPHTTP},
		},
		"unknown exporter": {
			config:  tracing.Config{Enabled: true, Exporter: "jaeger"},
			invalid: true,
		},
		// Checked while disabled too, which is the point: a typo waiting in a
		// switched-off section is found now rather than on the day someone
		// switches it on.
		"unknown exporter while disabled": {
			config:  tracing.Config{Exporter: "jaeger"},
			invalid: true,
		},
		"ratio at zero":               {config: tracing.Config{Enabled: true, SampleRatio: ratio(0)}},
		"ratio at one":                {config: tracing.Config{Enabled: true, SampleRatio: ratio(1)}},
		"ratio below zero":            {config: tracing.Config{Enabled: true, SampleRatio: ratio(-0.1)}, invalid: true},
		"ratio above one":             {config: tracing.Config{Enabled: true, SampleRatio: ratio(1.5)}, invalid: true},
		"negative timeout":            {config: tracing.Config{Enabled: true, TimeoutMS: -1}, invalid: true},
		"zero timeout is the default": {config: tracing.Config{Enabled: true, TimeoutMS: 0}},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestConfigDefaults pins what an enabled section that says nothing else
// resolves to — including the one value a reader is most likely to get wrong.
func TestConfigDefaults(t *testing.T) {
	cfg := tracing.Config{Enabled: true}

	if got := cfg.ExporterName(); got != tracing.ExporterOTLPGRPC {
		t.Errorf("ExporterName() = %v, want %v", got, tracing.ExporterOTLPGRPC)
	}
	if got := cfg.Service(); got != tracing.DefaultServiceName {
		t.Errorf("Service() = %v, want %v", got, tracing.DefaultServiceName)
	}
	if got := cfg.Ratio(); got != tracing.DefaultSampleRatio {
		t.Errorf("Ratio() = %v, want %v", got, tracing.DefaultSampleRatio)
	}
	if got := cfg.Timeout(); got != tracing.DefaultTimeout {
		t.Errorf("Timeout() = %v, want %v", got, tracing.DefaultTimeout)
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
	cfg := tracing.Config{Enabled: true, SampleRatio: &zero}

	if got := cfg.Ratio(); got != 0 {
		t.Errorf("Ratio() = %v, want 0", got)
	}
}

func TestConfigOverrides(t *testing.T) {
	cfg := tracing.Config{
		Enabled:     true,
		Exporter:    tracing.ExporterOTLPHTTP,
		ServiceName: "shigola-qa",
		TimeoutMS:   250,
		Headers:     env.Dict{"x-scope-orgid": "tenant-a"},
	}

	if got := cfg.ExporterName(); got != tracing.ExporterOTLPHTTP {
		t.Errorf("ExporterName() = %v, want %v", got, tracing.ExporterOTLPHTTP)
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

// TestConfigValidateEndpoint is the regression guard for the shape that took a
// live deployment down to zero exported traces.
//
// The endpoint was a full URL, which is what a platform hands an operator, and
// WithEndpoint wants a host and port — so the exporter percent-encoded the
// whole URL as a host and failed on every export with
//
//	parse "http://https:%2F%2F…%2Fv1%2Ftraces/v1/traces": invalid port
//
// A URL is now accepted and routed to WithEndpointURL, and every shape that
// cannot work is refused at startup rather than per batch.
func TestConfigValidateEndpoint(t *testing.T) {
	type tcase struct {
		endpoint string
		insecure bool
		invalid  bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			cfg := tracing.Config{
				Enabled:  true,
				Endpoint: env.String(tc.endpoint),
				Insecure: env.Bool(tc.insecure),
			}

			err := cfg.Validate()

			if tc.invalid && err == nil {
				t.Errorf("Validate() = nil for endpoint %q, expected an error", tc.endpoint)
			}
			if !tc.invalid && err != nil {
				t.Errorf("Validate() = %v for endpoint %q, expected nil", err, tc.endpoint)
			}
		}
	}

	tests := map[string]tcase{
		"empty defers to the sdk": {endpoint: ""},
		"host and port":           {endpoint: "tempo:4317"},
		"host and port, insecure": {endpoint: "tempo:4317", insecure: true},
		"bare host":               {endpoint: "tempo"},
		// The value from the deployment that failed.
		"https url": {
			endpoint: "https://infra-monitoring-opentelemetry-collector.apps.example.io/v1/traces",
		},
		"http url with port":    {endpoint: "http://tempo:4318/v1/traces"},
		"http url and insecure": {endpoint: "http://tempo:4318/v1/traces", insecure: true},
		// Asking for opposite things: resolving it either way would mean
		// either sending in the clear or failing a handshake.
		"https url and insecure": {
			endpoint: "https://tempo/v1/traces",
			insecure: true,
			invalid:  true,
		},
		// The other half of the same mistake: a path with no scheme fails just
		// as mysteriously as a URL passed to WithEndpoint did.
		"path without a scheme": {endpoint: "tempo:4318/v1/traces", invalid: true},
		"unsupported scheme":    {endpoint: "grpc://tempo:4317", invalid: true},
		"no host":               {endpoint: "https://", invalid: true},
		"non-numeric port":      {endpoint: "tempo:htpp", invalid: true},
		"too many colons":       {endpoint: "tempo:4317:4318", invalid: true},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestConfigIsEndpointURL(t *testing.T) {
	type tcase struct {
		endpoint string
		want     bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			cfg := tracing.Config{Endpoint: env.String(tc.endpoint)}

			if got := cfg.IsEndpointURL(); got != tc.want {
				t.Errorf("IsEndpointURL() = %v for %q, want %v", got, tc.endpoint, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"empty": {endpoint: ""},
		// url.Parse would read this as scheme "tempo" with opaque path
		// "4318", which is why the test is on the scheme separator instead.
		"host and port": {endpoint: "tempo:4318"},
		"http url":      {endpoint: "http://tempo:4318/v1/traces", want: true},
		"https url":     {endpoint: "https://tempo/v1/traces", want: true},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
