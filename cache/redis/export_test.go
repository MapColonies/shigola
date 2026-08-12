package redis

import "github.com/go-spatial/tegola/cache"

// RedisKeyForTest exposes the composed key so a test can assert the prefix is
// applied without standing up a redis. There is no production reason to read it
// back — callers go through Set/Get/Purge.
func (rdc *RedisCache) RedisKeyForTest(key *cache.Key) string { return rdc.redisKey(key) }
