# RedisCache

This package implements shigola's cache interface for use with Redis.
If a redis instance is running locally with default configurations solely
for shigola simply include the following snippet in shigola's config file:

```toml
[cache]
type="redis"
```

## Properties

The rediscache config supports the following properties:

> [!IMPORTANT]
> Connecting to redis via `uri` is going to be the default from v0.22.0
> onwards. The properties `network`, `address`, `db` and `ssl`
> are deprecated.
>
> `password` is **not** deprecated alongside `uri`. When both are given, the
> `password` key wins over the credential in the uri — which is how you supply a
> password containing characters a URI cannot carry literally. See
> [Passwords with special characters](#passwords-with-special-characters).

- `uri` (string): protocol `redis://` or `rediss://` followed by `<user>:<password>@<host>:<port>/<database>`
- `network` (string): [Optional] Network type, either `tcp` or `unix`.
  Defaults to 'tcp'.
- `address` (string): [Optional] the address of the Redis instance in form
  of `ip:port`. Defaults to '127.0.0.1:6379'.
- `password` (string): [Optional] password for the Redis instance.
  Defaults to '' (no password). Takes precedence over a password in `uri` when
  the key is present — including when it is present and empty, which asks for no
  password rather than falling back to the uri's.
- `db` (int): [Optional] the database within the Redis instance to cache to.
- `max_zoom` (int): [Optional] the max zoom the cache should cache to.
  After this zoom, Set() calls will return before doing work.
- `ttl` (int): [Optional] the key ttl time in seconds. Defaults to 0
  (the key has no expiration time).
- `key_prefix` (string): [Optional] a string prepended to every cache key, so one
  Redis instance can be shared rather than dedicated to this cache. Defaults to ''
  (no prefix, i.e. keys are exactly what they were before this option existed).
  The prefix is concatenated verbatim — **supply your own separator**:
  `key_prefix = "shigola:"` gives keys like `shigola:mymap/mylayer/10/511/340`,
  whereas `key_prefix = "shigola"` gives `shigolamymap/mylayer/10/511/340`.
- `ssl` (bool): [Optional] encrypt connection to the Redis server.
  Defaults to false (no SSL/TLS)

## Passwords with special characters

A `uri` is parsed as a URL, so a password inside one is subject to URL rules and
must be percent-encoded. Unencoded, the outcome depends on the character:

| In the uri | Result |
| --- | --- |
| `^` `[` `]` `{` `}` `\|` `<` `>` `\` `"` space | startup fails with `net/url: invalid userinfo` |
| `%` | startup fails with `invalid URL escape` |
| `/` `?` | startup fails — the authority ends there |
| `#` | **truncates the uri at that point.** Usually a startup error, but when what remains still parses it silently yields the wrong password |
| `$` `@` `&` `!` `*` `(` `)` `+` `=` `:` `~` `,` `;` `'` | works unencoded |

Percent-encoding handles every case:

```bash
python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" 'Aa1^%$#!'
# Aa1%5E%25%24%23%21
```

```toml
[cache]
type = "redis"
uri  = "redis://:Aa1%5E%25%24%23%21@127.0.0.1:6379/0"
```

**Or skip the encoding entirely** by leaving the password out of the uri and
putting it in `password`, which is passed to redis verbatim:

```toml
[cache]
type     = "redis"
uri      = "redis://127.0.0.1:6379/0"
password = 'Aa1^%$#!'
```

Two things to know about the config file itself, independent of redis:

- **Use TOML literal strings (single quotes) for passwords.** In a basic string
  (double quotes) TOML processes escapes, so `\` has to be written `\\` and `"`
  as `\"`. Getting this wrong does not produce a helpful error: a password ending
  in a backslash escapes the closing quote and fails with
  `strings cannot contain newlines`, naming a problem that isn't there. A literal
  string passes everything through as typed — its only limit is that it cannot
  contain a single quote.
- **`${NAME}` is expanded, in either quoting style.** Shigola substitutes
  environment variables into any config string, so a password containing a literal
  `${ALLCAPS}` is rewritten — or fails startup with
  `environment variable "NAME" not found`. Only that exact shape triggers it: a
  bare `$`, `$name`, or `${lowercase}` is left alone. If your password genuinely
  contains `${ALLCAPS}`, pass the whole password through an environment variable
  instead: `password = "${REDIS_PASSWORD}"`.
