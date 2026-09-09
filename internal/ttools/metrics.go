package ttools

import (
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
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
