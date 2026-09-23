package tracing

import (
	"fmt"
	"time"

	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/otlp"
)

// Exporter names accepted by Config.Exporter. See internal/otlp, which every
// OTLP export shares.
const (
	ExporterOTLPGRPC = otlp.ExporterGRPC
	ExporterOTLPHTTP = otlp.ExporterHTTP
)

// Defaults applied to an enabled Config that leaves a field unset.
const (
	DefaultServiceName = otlp.DefaultServiceName
	DefaultExporter    = otlp.DefaultExporter

	// DefaultSampleRatio is 1%, and it is deliberately not 100%.
	//
	// A tile server answers thousands of requests a second, and each tile
	// request produces a span per cache tier plus the provider query and the
	// encode — so head sampling everything multiplies request rate by the
	// span tree's width before it reaches the exporter, the network and
	// Tempo's ingesters. 1% keeps a busy service's tracing bill bounded while
	// still giving a few hundred complete traces a minute to look at.
	//
	// It applies only to traces this service *starts*: see the sampler in
	// tracing/otlp, which always follows an upstream decision.
	DefaultSampleRatio = 0.01

	DefaultTimeout = otlp.DefaultTimeout
)

// Config is the [tracing] section of the config file.
//
// Separate from [observer] on purpose: that section configures metrics, which
// Mimir scrapes, and the two have nothing to say to each other. Tracing is off
// unless this section says otherwise, so a config that predates it behaves
// exactly as it did.
type Config struct {
	// Enabled turns tracing on. Absent or false installs the no-op backend.
	Enabled env.Bool `toml:"enabled"`

	// Exporter selects the OTLP transport: "otlp_grpc" (default) or
	// "otlp_http".
	Exporter env.String `toml:"exporter"`

	// Endpoint is the collector to export to, in either of two shapes: a bare
	// "tempo:4317" host and port, or a full URL such as
	// "https://collector.example/v1/traces".
	//
	// Both work for both exporters, and they reach them through different
	// options — see endpointFor. Getting that wrong is not a loud failure,
	// which is why the shape is checked at startup: see validateEndpoint.
	//
	// Empty hands the decision to the OTEL SDK, which reads
	// OTEL_EXPORTER_OTLP_ENDPOINT (and its per-signal variants) and otherwise
	// defaults to localhost. That fallback is the OTEL specification's, not
	// shigola's, which is why it is not a SHIGOLA_-prefixed variable: a
	// sidecar collector is configured the same way for every service in the
	// mesh, and diverging from that would surprise whoever deploys this one.
	Endpoint env.String `toml:"endpoint"`

	// Insecure sends to the endpoint without TLS. Normal for an in-cluster
	// collector reached over the pod network; wrong across anything else.
	//
	// Ignored when Endpoint is a full URL, because the URL's scheme has
	// already said. Setting insecure alongside an https endpoint is a
	// contradiction rather than an override, and Validate rejects it.
	Insecure env.Bool `toml:"insecure"`

	// SampleRatio is the fraction of locally-started traces to record, 0 to 1.
	//
	// A pointer because 0 is a meaningful value that is not the default:
	// "record nothing this service starts, but still record a trace an
	// upstream service already decided to sample". Unset means
	// DefaultSampleRatio.
	SampleRatio *env.Float `toml:"sample_ratio"`

	// ServiceName is the service.name spans are attributed to.
	ServiceName env.String `toml:"service_name"`

	// TimeoutMS bounds one export attempt, in milliseconds.
	TimeoutMS env.Int `toml:"timeout_ms"`

	// Headers are sent with each export request — an authorization header for
	// a hosted collector, a tenant id for a multi-tenant Tempo.
	Headers env.Dict `toml:"headers"`
}

// Validate reports a [tracing] section that cannot be honoured.
//
// It runs whether or not tracing is enabled, so a typo in a section someone is
// about to switch on is a startup error now rather than the first time they
// switch it on. Nothing it checks can be valid-while-disabled and invalid
// while enabled, so this cannot reject a config that works today.
func (c Config) Validate() error {
	// Against the same table newExporter dispatches on, so the set of names
	// this accepts and the set it can actually build cannot drift apart. An
	// empty name means "the default", which is why it is allowed through here.
	if c.Exporter != "" {
		if _, ok := exporterFor[string(c.Exporter)]; !ok {
			return fmt.Errorf("tracing: exporter (%v) must be one of %v", c.Exporter, exporterNames())
		}
	}

	if c.SampleRatio != nil {
		if ratio := float64(*c.SampleRatio); ratio < 0 || ratio > 1 {
			return fmt.Errorf("tracing: sample_ratio (%v) must be between 0 and 1", ratio)
		}
	}

	if int(c.TimeoutMS) < 0 {
		return fmt.Errorf("tracing: timeout_ms (%v) must not be negative", int(c.TimeoutMS))
	}

	return c.validateEndpoint()
}

// IsEndpointURL reports whether Endpoint is a full URL rather than a bare host
// and port. See otlp.IsEndpointURL.
func (c Config) IsEndpointURL() bool {
	return otlp.IsEndpointURL(string(c.Endpoint))
}

// validateEndpoint rejects an endpoint neither exporter can use. See
// otlp.ValidateEndpoint for why it is checked at startup.
func (c Config) validateEndpoint() error {
	return otlp.ValidateEndpoint("tracing", string(c.Endpoint), bool(c.Insecure))
}

// ExporterName returns the configured exporter, or the default.
func (c Config) ExporterName() string {
	if c.Exporter == "" {
		return DefaultExporter
	}

	return string(c.Exporter)
}

// Service returns the service.name to attribute spans to, or the default.
func (c Config) Service() string {
	if c.ServiceName == "" {
		return DefaultServiceName
	}

	return string(c.ServiceName)
}

// Ratio returns the head-sampling ratio, or the default.
func (c Config) Ratio() float64 {
	if c.SampleRatio == nil {
		return DefaultSampleRatio
	}

	return float64(*c.SampleRatio)
}

// Timeout returns the per-export timeout, or the default.
func (c Config) Timeout() time.Duration {
	return otlp.Timeout(c.TimeoutMS)
}

// HeaderMap flattens Headers for the exporter, which takes map[string]string.
func (c Config) HeaderMap() map[string]string {
	return otlp.HeaderMap(c.Headers)
}
