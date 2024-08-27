# Deployment example

You can start up a example environment using Docker Compose in this directory.

First, start the services:

    docker compose up --build

This will start the proxy, and 4 instances of a simple web server. You can run
proxy commands with `docker compose exec proxy ...`. For example, to deploy the
first web server as a new service:

    docker compose exec proxy kamal-proxy deploy service1 --target example-web-1

And then access the proxy from a browser at http://localhost/.

Or, to list the currently deployed services:

    docker compose exec proxy kamal-proxy ls
