# Server

The server package is responsible for handling webserver requests for map tiles and various JSON endpoints describing the configured server. Example config:

```toml
[webserver]
port = ":9090"              # set something different than default ":8080"
ssl_cert = "fullchain.pem"  # ssl cert for serving by https
ssl_key = "privkey.pem"     # ssl key for serving by https


[webserver.headers]
Access-Control-Allow-Origin = "*"
```

### Config properties

- `port` (string): [Optional] Port and bind string. For example ":9090" or "127.0.0.1:9090". Defaults to ":8080"
- `hostname` (string): [Optional] The hostname to use in the various JSON endpoints. This is useful if shigola is behind a proxy and can't read the API consumer's request host directly.
- `uri_prefix` (string): [Optional] A prefix to add to all API routes. This is useful when shigola is behind a proxy (i.e. example.com/shigola). The prexfix will be added to all URLs included in the capabilities endpoint responses.
- `ssl_cert` (string): [Optional, unless ssl_key provided] Path to a certificate file for serving through HTTPS
- `ssl_key` (string): [Optional, unless ssl_cert provided] Path to a private key file for serving through HTTPS
