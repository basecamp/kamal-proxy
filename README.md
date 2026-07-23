# Kamal Proxy - A minimal HTTP proxy for zero-downtime deployments


## What it does

Kamal Proxy is a tiny HTTP proxy, designed to make it easy to coordinate
zero-downtime deployments. By running your web applications behind Kamal Proxy,
you can deploy changes to them without interrupting any of the traffic that's in
progress. No particular cooperation from an application is required for this to
work.

Kamal Proxy is designed to work as part of [Kamal](https://kamal-deploy.org/),
which provides a complete deployment experience including container packaging
and provisioning. However, Kamal Proxy could also be used standalone or as part
of other deployment tooling.


## A quick overview

To run an instance of the proxy, use the `kamal-proxy run` command. There's no
configuration file, but there are some options you can specify if the defaults
aren't right for your application.

For example, to run the proxy on a port other than 80 (the default) you could:

    kamal-proxy run --http-port 8080

Run `kamal-proxy help run` to see the full list of options.

To route traffic through the proxy to a web application, you `deploy` instances
of the application to the proxy. Deploying an instance makes it available to the
proxy, and replaces the instance it was using before (if any).

Use the format `hostname:port` when specifying the instance to deploy.

For example:

    kamal-proxy deploy service1 --target web-1:3000

This will instruct the proxy to register `web-1:3000` to receive traffic under
the service name `service1`. It will immediately begin running HTTP health
checks to ensure it's reachable and working and, as soon as those health checks
succeed, will start routing traffic to it.

If the instance fails to become healthy within a reasonable time, the `deploy`
command will stop the deployment and return a non-zero exit code, allowing
deployment scripts to handle the failure appropriately.

Each deployment takes over all the traffic from the previously deployed
instance. As soon as Kamal Proxy determines that the new instance is healthy,
it will route all new traffic to that instance.

### Opt-in scale to zero

Services can stop their write containers after an idle period and wake them on
the next application request:

    kamal-proxy run --docker-socket /var/run/docker.sock
    kamal-proxy deploy service1 --target web-1:3000 --idle-timeout 15m --idle-wake-timeout 30s

`--idle-timeout` defaults to `0` (disabled). `--idle-wake-timeout` defaults to
`30s` and bounds how long each request waits for Docker start and a successful
configured health check. The target hostname (`web-1` above) must be the Docker
container name. `DOCKER_SOCKET` and `KAMAL_PROXY_DOCKER_SOCKET` are equivalents
of the run flag.

Requests are held before their bodies are read, so POST bodies are forwarded
unchanged after a successful wake. Concurrent wake requests are coalesced.
Open streaming responses and WebSockets count as activity/in-flight work and
prevent sleeping until they close; a new stream or WebSocket is held during
wake like any other request. Health-check requests do not wake or reset an idle
service and receive success while it is stopping, sleeping, or waking.

Mounting the Docker socket gives the proxy host-level container control. Only
enable this feature where that trust is acceptable; the lifecycle calls are
isolated behind the `ContainerLifecycle` interface so they can be moved to an
external service later.

The Docker client negotiates the API version once from the daemon's unversioned
`/version` endpoint and caches it for start/stop calls. If that endpoint is
unavailable or returns a non-success status, it falls back to the legacy
`v1.41` paths for compatibility with restricted socket proxies; a successful
but malformed version response is rejected instead of guessing.

The `deploy` command also waits for traffic to drain from the old instance before
returning. This means it's safe to remove the old instance as soon as `deploy`
returns successfully, without interrupting any in-flight requests.

Because traffic is only routed to a new instance once it's healthy, and traffic
is drained completely from old instances before they are removed, deployments
take place with zero downtime.

### Customizing the health check

By default, Kamal Proxy will test the health of each service by sending a `GET`
request to `/up`, once per second. A `200` response is considered healthy.

If you need to customize the health checks for your application, there are a
few `deploy` flags you can use. See the help for `--health-check-path`,
`--health-check-port`, `--health-check-timeout`, and `--health-check-interval`.

For example, to change the health check path to something other than `/up`, you
could:

    kamal-proxy deploy service1 --target web-1:3000 --health-check-path web/index.html

To configure health checks to run on a different port than your main service
(useful when your app exposes health endpoints on a dedicated port), you could:

    kamal-proxy deploy service1 --target web-1:3000 --health-check-port 8080

### Host-based routing

Host-based routing allows you to run multiple applications on the same server,
using a single instance of Kamal Proxy to route traffic to all of them.

When deploying an instance, you can specify a host that it should serve traffic
for:

    kamal-proxy deploy service1 --target web-1:3000 --host app1.example.com

When deployed in this way, the instance will only receive traffic for the
specified host. By deploying multiple instances, each with their own host, you
can run multiple applications on the same server without port conflicts.

Only one service at a time can route a specific host:

    kamal-proxy deploy service1 --target web-1:3000 --host app1.example.com
    kamal-proxy deploy service2 --target web-2:3000 --host app1.example.com # returns "Error: host is used by another service"
    kamal-proxy remove service1
    kamal-proxy deploy service2 --target web-2:3000 --host app1.example.com # succeeds


### Path-based routing

For applications that split their traffic to different services based on the
request path, you can use path-based routing to mount services under different
path prefixes.

For example, to send all the requests for paths begining with `/api` to web-1,
and the rest to web-2:

    kamal-proxy deploy service1 --target web-1:3000 --path-prefix=/api
    kamal-proxy deploy service2 --target web-2:3000

By default, the path prefix will be stripped from the request before it is
forwarded upstream. So in the example above, a request to `/api/users/123` will
be forwarded to `web-1` as `/users/123`. To instead forward the request with
the original path (including the prefix), specify `--strip-path-prefix=false`:

    kamal-proxy deploy service1 --target web-1:3000 --path-prefix=/api --strip-path-prefix=false


### Excluding paths from metrics

When metrics are enabled (with `--metrics-port`), every request handled by
the proxy is recorded in the Prometheus output. High-volume traffic from
upstream load balancers or uptime monitors hitting health endpoints can
both inflate the metrics pipeline and dominate aggregate measures like
request rate, latency percentiles, and error rates, making the resulting
metrics a poor reflection of real user traffic.

To exclude one or more paths from the metrics for a service, use
`--exclude-metrics-path` when deploying. The flag may be repeated, and
matches are exact:

    kamal-proxy deploy service1 --target web-1:3000 --exclude-metrics-path /up --exclude-metrics-path /healthz

Excluded requests are still logged; only the Prometheus counters and
in-flight gauge are skipped.

Paths are specified as the upstream receives them. Services deployed using
stripped path prefixes should specify their excluded paths in the un-prefixed
form.


### Automatic TLS

Kamal Proxy can automatically obtain and renew TLS certificates for your
applications. To enable this, add the `--tls` flag when deploying an instance:

    kamal-proxy deploy service1 --target web-1:3000 --host app1.example.com --tls

Automatic TLS requires that hosts are specified (to ensure that certificates
are not maliciously requests for arbitrary hostnames).

Additionally, when using path-based routing, TLS options must be set on the
root path. Services deployed to other paths on the same host will use the same
TLS settings as those specified for the root path.


### On-demand TLS

Instead of specifying a static list of hosts, Kamal Proxy can also obtain TLS
certificates dynamically, for any host approved by an HTTP endpoint of your
choice. This is useful when the full set of hosts is not known at deploy time,
such as when serving customer domains.

To enable this, specify `--tls-on-demand-url` (instead of `--host`) when
deploying:

    kamal-proxy deploy service1 --target web-1:3000 --tls --tls-on-demand-url="http://localhost:4567/check"

The URL may be:

- An external URL (like `http://localhost:4567/check`), which Kamal Proxy will
  call directly, or
- A path (like `/check`), which Kamal Proxy will route through the service to
  your application, letting the application decide which hosts to allow.

Before issuing a certificate for a host, Kamal Proxy will send a `GET` request
to the endpoint, with the hostname in a `host` query parameter (for example,
`?host=app1.example.com`) and matching `Host` header. A `200` response allows
certificate issuance; any other response denies it, and the status code and up
to 256 bytes of the response body are logged to help with debugging. Checks
time out after 2 seconds, denying issuance for that attempt.


### Custom TLS certificate

When you obtained your TLS certificate manually, manage your own certificate authority,
or need to install Cloudflare origin certificate, you can manually specify path to
your certificate file and the corresponding private key:

    kamal-proxy deploy service1 --target web-1:3000 --host app1.example.com --tls --tls-certificate-path cert.pem --tls-private-key-path key.pem


## Specifying `run` options with environment variables

In some environments, like when running a Docker container, it can be convenient
to specify `run` options using environment variables. This avoids having to
update the `CMD` in the Dockerfile to change the options. To support this,
`kamal-proxy run` will read each of its options from environment variables if they
are set. For example, setting the HTTP port can be done with either:

    kamal-proxy run --http-port 8080

or:

    HTTP_PORT=8080 kamal-proxy run

If any of the environment variables conflict with something else in your
environment, you can prefix them with `KAMAL_PROXY_` to disambiguate them. For
example:

    KAMAL_PROXY_HTTP_PORT=8080 kamal-proxy run


## Building

To build Kamal Proxy locally, if you have a working Go environment you can:

    make

Alternatively, build as a Docker container:

    make docker


## Trying it out

See the [example](./example) folder for a Docker Compose setup that you can use
to try out the proxy commands.
