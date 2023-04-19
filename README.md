# mproxy - A minimal HTTP proxy for zero-downtime deployments

## Introduction

`mproxy` is a simple HTTP proxy designed to make it easy to coordinate
zero-downtime deployments.

By running your service behind an `mproxy` instance, you can add and remove
instances of your service without dropping any active connections. The process
for deploying a new service instance is essentially:

- Start a new service instance (e.g. a container)
- `mproxy add {new host}` (this blocks until the new instance is ready to accept
  traffic)
- `mproxy remove {old host}` (this blocks until the old instance has
  been drained of active traffic)
- Stop the old service instance

You can also combine the add & remove steps by using the `deploy` action to
specify the host(s) that should become active. Any other hosts not in the list
will be drained and removed:

    mproxy deploy {new host} {...new host}

## Trying it out

You can try out adding & removing hosts using the docker compose setup. Start it
with:

    docker compose up

One it starts, view the running containers:

    docker compose ps

You should see an instance of the `mproxy` container, as well as 4 instances of
a sample web service. Docker Compose will have named the web services
`mproxy-web-1`, `mproxy-web-2`, and so on.

The mproxy service is exposed locally as port 8000. But at this point no web
instances have been added to the `mproxy` service, so accessing it will result
in a 503 response.

In order to serve the web service traffic through the proxy, we'll need to add
at least one of the web instances. You can run `mproxy` commands in the proxy
container using `docker compose exec`:

    docker compose exec mproxy mproxy deploy mproxy-web-1:3000

You should see some log output from the proxy with the progress:

    {"level":"info","host":"mproxy-web-1:3000","time":"2023-04-18T21:28:47Z","message":"Service added"}
    {"level":"info","host":"mproxy-web-1:3000","from":"adding","to":"healthy","time":"2023-04-18T21:28:47Z","message":"Service health updated"}
    {"level":"info","host":"mproxy-web-1:3000","time":"2023-04-18T21:28:47Z","message":"Service is now healthy"}

You can now point a browser to http://localhost:8000/ to see the output from the web service.

To switch traffic to a new web instance, deploy that instance:

    docker compose exec mproxy mproxy deploy mproxy-web-2:3000

Which will add the new service instance, and drain the old one:

    {"level":"info","host":"mproxy-web-2:3000","time":"2023-04-19T04:31:37Z","message":"Service added"}
    {"level":"info","host":"mproxy-web-2:3000","from":"adding","to":"healthy","time":"2023-04-19T04:31:37Z","message":"Service health updated"}
    {"level":"info","host":"mproxy-web-2:3000","time":"2023-04-19T04:31:37Z","message":"Service is now healthy"}
    {"level":"info","host":"mproxy-web-1:3000","time":"2023-04-19T04:31:37Z","message":"Draining service"}
    {"level":"info","host":"mproxy-web-1:3000","time":"2023-04-19T04:31:37Z","message":"Removed service"}

By using a tool like `ab` to consume the service while swapping containers, you
can verify that no requests are dropped during the process, and that there are
no latency spikes.

You can add multiple active instances, in which case the proxy will round-robin
the traffic between them.

You can also list the instances registered in the proxy, along with their status:

    $ docker compose exec mproxy mproxy list
    mproxy-web-1:3000        (healthy)
    mproxy-web-2:3000        (healthy)
    mproxy-web-3:3000        (healthy)
