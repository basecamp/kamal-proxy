# mproxy - A minimal HTTP proxy for zero-downtime deployments

## What it does

`mproxy` is a tiny HTTP proxy, designed to make it easy to coordinate
zero-downtime deployments. By running a web application behind `mproxy` you can
deploy changes to it without interruping any of the traffic that's in progress.
No particular cooperation from the application is required for this to work.

## A quick overview

To run an instance of the proxy, use the `mproxy run` command. There's no
configuration file, but there are some options you can specify if the defaults
aren't right for your application.

For example, to run the proxy on a port other than 80 (the default) you could:

    mproxy run --port 8080

Run `mproxy help run` to see the full list of options.

To route traffic through the proxy to a web application, you `deploy` instances of
the application to the proxy. Deploying instances makes them available to the proxy,
and also replaces any previous instances that are no longer being used.

Use the format `hostname:port` when specifying the application instances. You
can specify one or more instances in each deployment; if there's more than one,
the traffic will be load-balanced between all of them.

For example:

    mproxy deploy web-1:3000

This will instruct the proxy to register `web-1:3000` to receive traffic. It
will immediately begin running HTTP health checks to ensure it's reachable and
working and, as soon as those health checks succeed, will start routing traffic
to it.

If an instance fails to become healthy within the allowed time, `deploy` returns
a non-zero exit code, so that deployment scripts can handle the failure
appropriately.

To deploy a new version of an application, call `deploy` again with the new
instance(s) that should replace those currently running. For example:

    mproxy deploy web-2:3000 web-3:3000

This will do 2 things:

- First, it will add the new instances, and wait for them to be healthy, in the
  same way as before.
- Second, any instances that were previously running, but are not listed in the
  new deployment (so in this example, that's `web-1:3000`) will be considered
  outdated. They'll stop receiving new traffic, and will be given some time to
  drain any requests that are in flight. As soon as the draining is complete,
  they'll be removed from the list.

Processing the steps in this order ensures that there's no downtime or failed
requests during the deployment.

If you need more control of the timing or sequence of adding and removing
instances, you can use the `add` and `rm` commands to perform the steps
individually. For example:

    mproxy add web-4:3000

or:

    mproxy rm web-{5,6,7}

Lastly, you can list the currently registered instances, along with their
status:

    $ mproxy list
    web-2:3000        (healthy)
    web-3:3000        (healthy)
    web-4:3000        (unhealthy)
    web-5:3000        (adding)

## Building

To build `mproxy` locally, if you have a working Go environment you can:

    make

Alternatively, build as a Docker container:

    make docker

## Trying it out

You can start up a sample environment to try it out using Docker Compose:

    docker compose up --build

This will start the proxy, and 4 instances of a simple web server. You can run
proxy commands with `docker compose exec proxy ...`, for example:

    docker compose exec proxy mproxy deploy mproxy-web-1:3000

And then access the proxy from a browser at http://localhost:8000/.
