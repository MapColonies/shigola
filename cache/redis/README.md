# RedisCache

This package implements tegola's cache interface for use with Redis.
If a redis instance is running locally with default configurations solely
for tegola simply include the following snippet in tegola's config file:

```toml
[cache]
type="redis"
```

## Properties

The rediscache config supports the following properties:

> [!IMPORTANT]
> Connecting to redis via `uri` is going to be the default from v0.22.0
> onwards. The properties `network`, `address`, `password` `db` and `ssl`
> are deprecated.

- `uri` (string): protocol `redis://` or `rediss://` followed by `<user>:<password>@<host>:<port>/<database>`
- `network` (string): [Optional] Network type, either `tcp` or `unix`.
  Defaults to 'tcp'.
- `address` (string): [Optional] the address of the Redis instance in form
  of `ip:port`. Defaults to '127.0.0.1:6379'.
- `password` (string): [Optional] password for the Redis instance.
  Defaults to '' (no password).
- `db` (int): [Optional] the database within the Redis instance to cache to.
- `max_zoom` (int): [Optional] the max zoom the cache should cache to.
  After this zoom, Set() calls will return before doing work.
- `ttl` (int): [Optional] the key ttl time in seconds. Defaults to 0
  (the key has no expiration time).
- `key_prefix` (string): [Optional] a string prepended to every cache key, so one
  Redis instance can be shared rather than dedicated to this cache. Defaults to ''
  (no prefix, i.e. keys are exactly what they were before this option existed).
  The prefix is concatenated verbatim — **supply your own separator**:
  `key_prefix = "tegola:"` gives keys like `tegola:mymap/mylayer/10/511/340`,
  whereas `key_prefix = "tegola"` gives `tegolamymap/mylayer/10/511/340`.
- `ssl` (bool): [Optional] encrypt connection to the Redis server.
  Defaults to false (no SSL/TLS)
