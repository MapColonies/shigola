// Package otlp holds what the OTLP exporters shigola builds have in common,
// whatever the signal: the choice of transport, the endpoint and its
// validation, the headers, and the resource describing the process.
//
// Extracted from tracing when logs gained an OTLP export of their own. The
// endpoint check in particular exists because getting the shape wrong fails
// silently, per export, and a second copy of it would be a second place for
// that to regress.
package otlp

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/MapColonies/shigola/internal/build"
	"github.com/MapColonies/shigola/internal/env"
)

// Exporter names a config section may choose between.
//
// Tempo and Loki listen for OTLP on both, on different ports (4317 and 4318),
// and a collector in front of them may accept only one — so the protocol is an
// operator decision rather than something this service can pick for them.
const (
	ExporterGRPC = "otlp_grpc"
	ExporterHTTP = "otlp_http"
)

// Defaults for a section that leaves a field unset.
const (
	// DefaultServiceName is what telemetry is attributed to when the config
	// does not say. It is the process, not the deployment: an operator running
	// several shigolas against one backend should set service_name per
	// deployment.
	DefaultServiceName = "shigola"

	// DefaultExporter is OTLP over gRPC — the cheaper of the two per item.
	DefaultExporter = ExporterGRPC

	// DefaultTimeout bounds one export attempt. Long enough to ride out a
	// collector's GC pause, short enough that a black-holed endpoint does not
	// pile items up in the batch processor's queue indefinitely.
	DefaultTimeout = 10 * time.Second
)

// IsEndpointURL reports whether endpoint is a full URL rather than a bare host
// and port.
//
// On the scheme separator, not on url.Parse succeeding: url.Parse accepts
// "tempo:4318" quite happily, reading "tempo" as the scheme and "4318" as an
// opaque path, so it cannot tell the two shapes apart.
func IsEndpointURL(endpoint string) bool {
	return strings.Contains(endpoint, "://")
}

// ValidateEndpoint rejects an endpoint neither exporter can use. section names
// the config section it came from, for the error.
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
func ValidateEndpoint(section, endpoint string, insecure bool) error {
	if endpoint == "" {
		// Left to the SDK, which reads OTEL_EXPORTER_OTLP_ENDPOINT and
		// otherwise defaults to localhost.
		return nil
	}

	if IsEndpointURL(endpoint) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return fmt.Errorf("%v: endpoint (%v) is not a valid URL: %w", section, endpoint, err)
		}

		switch u.Scheme {
		case "http", "https":
		default:
			return fmt.Errorf("%v: endpoint (%v) has scheme %q, want http or https", section, endpoint, u.Scheme)
		}

		if u.Host == "" {
			return fmt.Errorf("%v: endpoint (%v) names no host", section, endpoint)
		}

		// Rejected rather than resolved either way: the two keys are asking
		// for opposite things, and silently picking one would mean either
		// sending credentials in the clear or failing a handshake, depending
		// on which.
		if u.Scheme == "https" && insecure {
			return fmt.Errorf("%v: endpoint (%v) is https but insecure is true; drop one", section, endpoint)
		}

		return nil
	}

	// No scheme, so this is the host-and-port shape, which carries no path.
	// A path here is the other half of the same mistake — "tempo:4318/v1/traces"
	// fails exactly as mysteriously as the URL did.
	if strings.ContainsAny(endpoint, "/?#") {
		return fmt.Errorf("%v: endpoint (%v) has a path but no scheme; write host:port, or a full URL", section, endpoint)
	}

	if strings.Contains(endpoint, ":") {
		_, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return fmt.Errorf("%v: endpoint (%v) is not host:port: %w", section, endpoint, err)
		}

		// SplitHostPort only splits; it does not check that the port is a
		// number, so "tempo:htpp" reaches it intact and fails at dial time.
		if _, err := strconv.Atoi(port); err != nil {
			return fmt.Errorf("%v: endpoint (%v) has a non-numeric port (%v)", section, endpoint, port)
		}
	}

	return nil
}

// Timeout converts a section's timeout_ms, applying DefaultTimeout when it is
// unset.
func Timeout(ms env.Int) time.Duration {
	if ms <= 0 {
		return DefaultTimeout
	}

	return time.Duration(ms) * time.Millisecond
}

// HeaderMap flattens a section's headers for an exporter, which takes
// map[string]string.
func HeaderMap(headers env.Dict) map[string]string {
	if len(headers) == 0 {
		return nil
	}

	out := make(map[string]string, len(headers))
	for name, value := range headers {
		out[name] = fmt.Sprintf("%v", value)
	}

	return out
}

// Resource describes this process to a collector, as service.name serviceName
// and service.version the build's version.
//
// Merged onto resource.Default() rather than replacing it, so the host,
// process and telemetry.sdk attributes an operator expects to filter on are
// still there. The schema URL is pinned to the same semconv version the SDK's
// own resource package uses; a different one makes Merge report a schema
// conflict instead of merging.
func Resource(section, serviceName string) (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(build.Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("%v: describing this process: %w", section, err)
	}

	return res, nil
}
