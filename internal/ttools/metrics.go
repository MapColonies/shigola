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

// HistogramSample returns the one sample of the named histogram family whose labels
// include every pair in labels.
//
// Shared for the reason MetricFamilyNames is: three tests in three packages
// read a histogram back out of a registry to assert what an observation
// recorded, and the family-then-label walk was otherwise written out in each.
//
// gatherer rather than the default registry, because both are needed: a test
// that can build its own registry should, since the default one is
// process-wide and accumulates whatever else the binary registered, but a test
// going through atlas or a real request reaches the metrics only through the
// default registry the observer installs itself against.
func HistogramSample(t *testing.T, gatherer prometheus.Gatherer, name string, labels map[string]string) *dto.Histogram {
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
			if HasLabels(metric.GetLabel(), labels) {
				return metric.GetHistogram()
			}
		}
	}

	t.Fatalf("no sample of %v matches %v", name, labels)

	return nil
}

// ExemplarLabels returns the labels of the most recently stored exemplar on
// that histogram — the trace and span the caller's own observation named.
//
// "Most recently stored", not "the first bucket that has one", and the
// difference is a real test failure rather than a nicety. Prometheus keeps one
// exemplar *per bucket*, and a family on the process-wide registry outlives the
// test that observed into it — so two tests observing the same family leave two
// exemplars behind whenever their observations land in different buckets, and
// the lower bucket's is whichever happened to be faster rather than whichever
// was later. Taking the newest by timestamp makes a caller read back what it
// just recorded; scanning bucket order made that flaky under -race, where the
// spread between two observations is wide enough to separate them.
//
// Fatal when no bucket carries one: every caller is asserting that an exemplar
// was attached, and "absent" and "attached with the wrong labels" are different
// failures worth different messages. Use HistogramSample directly to assert the
// opposite, that nothing was attached.
func ExemplarLabels(t *testing.T, gatherer prometheus.Gatherer, name string, labels map[string]string) map[string]string {
	t.Helper()

	histogram := HistogramSample(t, gatherer, name, labels)

	var newest *dto.Exemplar
	for _, bucket := range histogram.GetBucket() {
		exemplar := bucket.GetExemplar()
		if exemplar == nil {
			continue
		}
		if newest == nil || exemplar.GetTimestamp().AsTime().After(newest.GetTimestamp().AsTime()) {
			newest = exemplar
		}
	}

	if newest == nil {
		t.Fatalf("no bucket of %v matching %v carries an exemplar", name, labels)

		return nil
	}

	got := make(map[string]string, len(newest.GetLabel()))
	for _, pair := range newest.GetLabel() {
		got[pair.GetName()] = pair.GetValue()
	}

	return got
}

// HasLabels reports whether pairs contain every label in want — a subset
// match, so a caller names only the labels it cares about and ignores whatever
// else the observe-vars added.
//
// Exported because the sample lookups here are not the only place that needs
// it: atlas's own counter() reader matches labels the same way.
func HasLabels(pairs []*dto.LabelPair, want map[string]string) bool {
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
