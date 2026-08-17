package multi

import "github.com/MapColonies/shigola/cache"

// WritePoolForTest exposes the injected pool so a test can assert that a nested
// chain shares its parent's rather than running its promotions inline. There is
// no production reason to read it back.
func (c *Cache) WritePoolForTest() *cache.WritePool { return c.writes }
