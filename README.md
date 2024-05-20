# parachute - A minimal HTTP proxy for zero-downtime deployments 🪂

## What it does

`parachute` is a tiny HTTP proxy, designed to make it easy to coordinate
zero-downtime deployments. By running a web application behind `parachute` you can
deploy changes to it without interruping any of the traffic that's in progress.
No particular cooperation from the application is required for this to work.


## A quick overview

To run an instance of the proxy, use the `parachute run` command. There's no
configuration file, but there are some options you can specify if the defaults
aren't right for your application.

For example, to run the proxy on a port other than 80 (the default) you could:

    parachute run --http-port 8080

Run `parachute help run` to see the full list of options.

To route traffic through the proxy to a web application, you `deploy` instances
of the application to the proxy. Deploying an instance makes it available to the
proxy, and replace the instance it was using before (if any).

Use the format `hostname:port` when specifying the instance to deploy.

For example:

    parachute deploy service1 --target web-1:3000

This will instruct the proxy to register `web-1:3000` to receive traffic under
the service name `service1. It will immediately begin running HTTP health checks
to ensure it's reachable and working and, as soon as those health checks succeed,
will start routing traffic to it.

If the instance fails to become healthy within a reasonable time, the `deploy`
command will stop the deployment and return a non-zero exit code, so that
deployment scripts can handle the failure appropriately.

Each deployment takes over traffic from the previously deployed instance. As
soon as parachute determines that the new instance is healthy, it will route all
new traffic to that instance.

The `deploy` command will wait for traffic to drain from the old instance before
returning. This means it's safe to remove the old instance as soon as `deploy`
returns successfully, without interrupting any in-flight requests.

Because traffic is only routed to a new instance once it's healthy, and traffic
is drained from old instances before they are removed, deployments take place
with zero downtime.

### Host-based routing

Host-based routing allows you to run multiple applications on the same server,
using a single instance of `parachute` to route traffic to all of them.

When deploying an instance, you can specify a host that it should serve traffic
for:

    parachute deploy service1 --target web-1:3000 --host app1.example.com

When deployed in this way, the instance will only receive traffic for the
specified host. By deploying multiple instances, each with their own host, you
can run multiple applications on the same server without port conflicts.

Only one service at a time can route a specific host:

    parachute deploy service1 --target web-1:3000 --host app1.example.com
    parachute deploy service2 --target web-2:3000 --host app1.example.com # returns "Error: host is used by another service"
    parachute remove service1
    parachute deploy service2 --target web-2:3000 --host app1.example.com # suceeds


### Automatic TLS

`parachute` can automatically obtain and renew TLS certificates for your
applications. To enable this, add the `--tls` flag when deploying an instance:

    parachute deploy service1 --target web-1:3000 --host app1.example.com --tls


## Building

To build `parachute` locally, if you have a working Go environment you can:

    make

Alternatively, build as a Docker container:

    make docker


## Trying it out

You can start up a sample environment to try it out using Docker Compose:

    docker compose up --build

This will start the proxy, and 4 instances of a simple web server. You can run
proxy commands with `docker compose exec proxy ...`, for example:

    docker compose exec proxy parachute deploy service1 --target parachute-web-1:3000

And then access the proxy from a browser at http://localhost/.

## Specifying `run` options with environment variables

In some environments, like when running a Docker container, it can be convenient
to specify `run` options using environment variables. This avoids having to
update the `CMD` in the Dockerfile to change the options. To support this,
`parachute run` will read each of its options from environment variables if they
are set. For example, setting the HTTP port can be done with either:

    parachute run --http-port 8080

or:

    HTTP_PORT=8080 parachute run

If any of the environment variables conflict with something else in your
environment, you can prefix them with `PARACHUTE_` to disambiguate them. For
example:

    PARACHUTE_HTTP_PORT=8080 parachute run
