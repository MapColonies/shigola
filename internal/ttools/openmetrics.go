package ttools

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// This file is the evidence for a claim the docs make and an operator acts on:
// which bucket boundaries are respelled by serving OpenMetrics, and therefore
// which `le` series change identity. Getting that list wrong understates an
// upgrade break — the first version of those docs named a boundary that is not
// affected at all, and the second missed two whole families — so it is derived
// rather than worked out by hand.
//
// Derived by *scraping both encoders* rather than by reimplementing the rule
// they apply. An earlier version copied expfmt's unexported
// writeOpenMetricsFloat, which put the docs one vendored change away from being
// wrong with the test still green; the copy also silently dropped that
// function's NaN and ±Inf cases. Nothing here knows the rule, so a change to it
// shows up as a failure naming the boundary that moved.
//
// It lives in ttools rather than beside the observer because the families it
// has to cover do not all live there. provider/postgis declares its own
// duration buckets, and a differ private to package prometheus could not see
// them — which is exactly how those two families came to be missing from the
// documented list while a test named "derives this from the bucket sets" stayed
// green.

// labelPattern pulls the values of one label out of an exposition line.
//
// Built here rather than taken as a *regexp.Regexp parameter: the two callers
// want a bucket boundary or a summary quantile, and a function promising to
// differ on any regexp at all would be promising more than it is asked for.
func labelPattern(label string) *regexp.Regexp {
	return regexp.MustCompile(label + `="([^"]+)"`)
}

// The two exposition formats, named so a call site says which it means rather
// than passing a bare true or false.
const (
	openMetricsText = true
	classicText     = false
)

// scrapeLabel serves a gatherer in one exposition format and returns the values
// pattern captures, in the order they were written.
func scrapeLabel(t *testing.T, gatherer prometheus.Gatherer, label string, openMetrics bool) []string {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if openMetrics {
		// The one Accept header that reaches the OpenMetrics encoder in this
		// vendored expfmt, which negotiates version 0.0.1 only.
		request.Header.Set("Accept", "application/openmetrics-text;version=0.0.1")
	}

	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{EnableOpenMetrics: openMetrics}).
		ServeHTTP(recorder, request)

	var found []string
	for _, match := range labelPattern(label).FindAllStringSubmatch(recorder.Body.String(), -1) {
		found = append(found, match[1])
	}

	return found
}

// Respelled serves gatherer under both exposition formats, pairs the label
// values written by each, and returns the ones the two spell differently as
// "before -> after".
//
// The pairing is positional, which is why the lengths are checked first: the
// two encoders write the same samples in the same order, and a length mismatch
// means they no longer do, which would make every comparison below meaningless
// rather than merely wrong.
func Respelled(t *testing.T, gatherer prometheus.Gatherer, label string) []string {
	t.Helper()

	classic := scrapeLabel(t, gatherer, label, classicText)
	openMetrics := scrapeLabel(t, gatherer, label, openMetricsText)

	if len(classic) == 0 {
		t.Fatal("no matching labels in the exposition; this test would prove nothing")
	}
	if len(classic) != len(openMetrics) {
		t.Fatalf("%d labels classic, %d under OpenMetrics; the two are not comparable",
			len(classic), len(openMetrics))
	}

	var changed []string
	for i := range classic {
		if classic[i] != openMetrics[i] {
			changed = append(changed, classic[i]+" -> "+openMetrics[i])
		}
	}

	return changed
}

// RespelledBuckets is Respelled over one histogram's bucket set, which is what
// every caller but the summary case wants.
//
// The single observation is what makes every bucket appear in the exposition;
// without it there is nothing to compare.
func RespelledBuckets(t *testing.T, buckets []float64) []string {
	t.Helper()

	registry := prometheus.NewRegistry()
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "test_le_seconds",
		Buckets: buckets,
	})
	registry.MustRegister(histogram)
	histogram.Observe(0)

	return Respelled(t, registry, "le")
}

// AssertRespelled compares what actually moved against what the docs say did.
func AssertRespelled(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("respelled = %v, documented %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("respelled %d = %q, documented %q", i, got[i], want[i])
		}
	}
}
