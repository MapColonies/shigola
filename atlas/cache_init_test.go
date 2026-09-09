package atlas

import (
	"reflect"
	"sort"
	"testing"

	"github.com/MapColonies/shigola/cache"
)

// TestCheckCacheTypes asserts the exact set of cache types the tree registers,
// which makes it sensitive to the fake types the other test files in this
// package register through cache.Register — a process-wide registry with no way
// to unregister.
//
// It passes because Go runs a package's tests in declaration order, files taken
// in sorted order, and every file that registers a fake sorts after this one.
// That is load-bearing: a new test file registering a fake type from a name
// sorting before "cache_init_test.go" fails this test rather than its own.
func TestCheckCacheTypes(t *testing.T) {
	c := cache.Registered()
	exp := []string{"azblob", "file", "multi", "redis", "s3", "gcs"}
	sort.Strings(exp)
	if !reflect.DeepEqual(c, exp) {
		t.Errorf("registered cachés, expected %v got %v", exp, c)
	}
}
