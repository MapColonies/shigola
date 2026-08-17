package cmd

import (
	"context"
	"reflect"
	"testing"
)

// TestOnCompleteRunsInReverseRegistrationOrder pins the property the cache
// write-pool drain's registration placement depends on.
//
// cmd/shigola/cmd/server.go registers the drain between observability.Cleanup
// and shutdown(srv) precisely because these run in reverse. If that ever
// changed, the drain would silently move to the wrong side of both — draining
// while requests are still arriving, or after the metrics reporting its outcome
// were already flushed — and neither failure produces a symptom.
func TestOnCompleteRunsInReverseRegistrationOrder(t *testing.T) {
	saved := ctx
	t.Cleanup(func() { ctx = saved })

	c := &contextType{}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	ctx = c

	var order []string
	// registered in the order cmd/shigola/cmd/server.go registers them
	c.OnComplete(func() { order = append(order, "provider") })
	c.OnComplete(func() { order = append(order, "observability") })
	c.OnComplete(func() { order = append(order, "drain") })
	c.OnComplete(func() { order = append(order, "shutdown") })

	c.Complete()

	expected := []string{"shutdown", "drain", "observability", "provider"}
	if !reflect.DeepEqual(order, expected) {
		t.Errorf("got %v, expected %v", order, expected)
	}
}

// TestOnCompleteRunsOneVariadicCallForwards records a subtlety that is easy to
// trip over and has no symptom when you do.
//
// Separate OnComplete calls run in reverse, but functions passed together in a
// *single* variadic call run in the order they were written: OnComplete appends
// them reversed, and Complete then reverses the whole slice, cancelling out.
//
// It matters because the reverse-registration property is load-bearing for the
// cache write-pool drain, and "tidying" a run of separate OnComplete calls into
// one variadic call would silently invert the shutdown order.
func TestOnCompleteRunsOneVariadicCallForwards(t *testing.T) {
	saved := ctx
	t.Cleanup(func() { ctx = saved })

	c := &contextType{}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	ctx = c

	var order []string
	c.OnComplete(
		func() { order = append(order, "first") },
		func() { order = append(order, "second") },
	)

	c.Complete()

	expected := []string{"first", "second"}
	if !reflect.DeepEqual(order, expected) {
		t.Errorf("got %v, expected %v", order, expected)
	}
}
