package tracing

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MapColonies/shigola/internal/env"
)

// Exporter names accepted by Config.Exporter.
//
// Tempo listens for OTLP on both, on different ports (4317 and 4318), and a
// collector in front of it may accept only one — so the protocol is an operator
// decision rather than something this service can pick for them.
const (
	ExporterOTLPGRPC = "otlp_grpc"
	ExporterOTLPHTTP = "otlp_http"
)

// Defaults applied to an enabled Config that leaves a field unset.
const (
	// DefaultServiceName is what spans are attributed to in Tempo when the
	// config does not say. It is the process, not the deployment: an operator
	// running several shigolas against one Tempo should set service_name per
	// deployment.
	DefaultServiceName = "shigola"

	// DefaultExporter is OTLP over gRPC — Tempo's own default receiver, and
	// the cheaper of the two per span.
	DefaultExporter = ExporterOTLPGRPC

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

	// DefaultTimeout bounds one export attempt. Long enough to ride out a
	// collector's GC pause, short enough that a black-holed endpoint does not
	// pile spans up in the batch processor's queue indefinitely.
	DefaultTimeout = 10 * time.Second
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
// and port.
//
// On the scheme separator, not on url.Parse succeeding: url.Parse accepts
// "tempo:4318" quite happily, reading "tempo" as the scheme and "4318" as an
// opaque path, so it cannot tell the two shapes apart.
func (c Config) IsEndpointURL() bool {
	return strings.Contains(string(c.Endpoint), "://")
}

// validateEndpoint rejects an endpoint neither exporter can use.
//
// This is checked at startup because getting it wrong does not fail at
// startup. Handing a URL to WithEndpoint, which wants host and port only,
// makes the exporter treat the whole string as a host — percent-encoding it,
// prefixing a scheme and appending the signal path — and then fail on every
// single export with
//
//	traces export: parse "http://https:%2F%2Fcollector.example%2Fv1%2Ftraces/v1/traces":
//	invalid port ":%2F%2Fcollector.example%2Fv1%2Ftraces" after host
//
// which is what a real deployment hit: a server healthy by every other signal,
// exporting nothing, for as long as nobody read the logs closely. The shape is
// decided here instead.
func (c Config) validateEndpoint() error {
	endpoint := string(c.Endpoint)
	if endpoint == "" {
		// Left to the SDK, which reads OTEL_EXPORTER_OTLP_ENDPOINT and
		// otherwise defaults to localhost.
		return nil
	}

	if c.IsEndpointURL() {
		u, err := url.Parse(endpoint)
		if err != nil {
			return fmt.Errorf("tracing: endpoint (%v) is not a valid URL: %w", endpoint, err)
		}

		switch u.Scheme {
		case "http", "https":
		default:
			return fmt.Errorf("tracing: endpoint (%v) has scheme %q, want http or https", endpoint, u.Scheme)
		}

		if u.Host == "" {
			return fmt.Errorf("tracing: endpoint (%v) names no host", endpoint)
		}

		// Rejected rather than resolved either way: the two keys are asking
		// for opposite things, and silently picking one would mean either
		// sending credentials in the clear or failing a handshake, depending
		// on which.
		if u.Scheme == "https" && bool(c.Insecure) {
			return fmt.Errorf("tracing: endpoint (%v) is https but insecure is true; drop one", endpoint)
		}

		return nil
	}

	// No scheme, so this is the host-and-port shape, which carries no path.
	// A path here is the other half of the same mistake — "tempo:4318/v1/traces"
	// fails exactly as mysteriously as the URL did.
	if strings.ContainsAny(endpoint, "/?#") {
		return fmt.Errorf("tracing: endpoint (%v) has a path but no scheme; write host:port, or a full URL", endpoint)
	}

	if strings.Contains(endpoint, ":") {
		_, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return fmt.Errorf("tracing: endpoint (%v) is not host:port: %w", endpoint, err)
		}

		// SplitHostPort only splits; it does not check that the port is a
		// number, so "tempo:htpp" reaches it intact and fails at dial time.
		if _, err := strconv.Atoi(port); err != nil {
			return fmt.Errorf("tracing: endpoint (%v) has a non-numeric port (%v)", endpoint, port)
		}
	}

	return nil
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
	if c.TimeoutMS <= 0 {
		return DefaultTimeout
	}

	return time.Duration(c.TimeoutMS) * time.Millisecond
}

// HeaderMap flattens Headers for the exporter, which takes map[string]string.
func (c Config) HeaderMap() map[string]string {
	if len(c.Headers) == 0 {
		return nil
	}

	headers := make(map[string]string, len(c.Headers))
	for name, value := range c.Headers {
		headers[name] = fmt.Sprintf("%v", value)
	}

	return headers
}
