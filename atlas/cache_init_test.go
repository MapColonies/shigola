package atlas

import (
	"reflect"
	"sort"
	"testing"

	"github.com/MapColonies/shigola/cache"
)

func TestCheckCacheTypes(t *testing.T) {
	c := cache.Registered()
	exp := []string{"azblob", "file", "multi", "redis", "s3", "gcs"}
	sort.Strings(exp)
	if !reflect.DeepEqual(c, exp) {
		t.Errorf("registered cachés, expected %v got %v", exp, c)
	}
}
