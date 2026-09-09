package ttools

import (
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// MetricFamilyNames is every metric family the process publishes right now,
// sorted.
//
// Shared because two tests in different packages assert the same claim from
// opposite ends — that enabling tracing leaves the metrics exactly as they
// were, checked over the cache path in atlas and over a real request in
// server — and the gather-and-sort was written out verbatim in both.
//
// It reads the default registry, which the prometheus observer registers
// against, so a caller comparing two snapshots has to make sure the work
// either side of them is equivalent: a *Vec publishes a family only once a
// label set on it has been observed, so a family can appear for reasons that
// have nothing to do with what is being tested.
func MetricFamilyNames(t *testing.T) []string {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
	}
	slices.Sort(names)

	return names
}

// Histogram returns the one sample of the named histogram family whose labels
// include every pair in labels.
//
// Shared for the reason MetricFamilyNames is: three tests in three packages
// read a histogram back out of a registry to assert what an observation
// recorded, and the family-then-label walk was otherwise written out in each.
//
// gatherer rather than the default registry, because a test asserting on
// exemplars usually wants a registry of its own — the default one is
// process-wide and accumulates whatever else the binary registered.
func Histogram(t *testing.T, gatherer prometheus.Gatherer, name string, labels map[string]string) *dto.Histogram {
	t.Helper()

	families, err := gatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, metric := range family.GetMetric() {
			if hasLabels(metric.GetLabel(), labels) {
				return metric.GetHistogram()
			}
		}
	}

	t.Fatalf("no sample of %v matches %v", name, labels)

	return nil
}

// ExemplarLabels returns the labels of the exemplar on the first bucket of that
// histogram to carry one — the trace and span a traced observation named.
//
// Fatal when no bucket carries one: every caller is asserting that an exemplar
// was attached, and "absent" and "attached with the wrong labels" are different
// failures worth different messages. Use Histogram directly to assert the
// opposite, that nothing was attached.
func ExemplarLabels(t *testing.T, gatherer prometheus.Gatherer, name string, labels map[string]string) map[string]string {
	t.Helper()

	histogram := Histogram(t, gatherer, name, labels)

	for _, bucket := range histogram.GetBucket() {
		exemplar := bucket.GetExemplar()
		if exemplar == nil {
			continue
		}

		got := make(map[string]string, len(exemplar.GetLabel()))
		for _, pair := range exemplar.GetLabel() {
			got[pair.GetName()] = pair.GetValue()
		}

		return got
	}

	t.Fatalf("no bucket of %v matching %v carries an exemplar", name, labels)

	return nil
}

// hasLabels reports whether pairs contain every label in want.
func hasLabels(pairs []*dto.LabelPair, want map[string]string) bool {
	for name, value := range want {
		found := false
		for _, pair := range pairs {
			if pair.GetName() == name && pair.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
