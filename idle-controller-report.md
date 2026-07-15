# Proxy idle controller implementation report

## Assumptions and success criteria

- Scale-to-zero is explicitly enabled per service only when `--idle-timeout` is greater than zero.
- Each write target hostname is also its Docker container name; ports are stripped before lifecycle calls.
- The proxy process can access the configured Docker Unix socket when the feature is enabled.
- Success means: never stop with an application request in flight; serialize stop versus arriving requests; coalesce concurrent wakes; hold requests until readiness, failure, cancellation, or timeout; preserve sleeping state over restart; and keep timeout-zero behavior unchanged.
- Health-check requests neither reset idle time nor wake a sleeping service. Paused/stopped services disable idle transitions; resume restarts the idle timer; deploy replaces the lifecycle target set and returns the controller to active.

## CLI contract

```text
kamal-proxy run [--docker-socket PATH]
  --docker-socket default: /var/run/docker.sock
  environment: DOCKER_SOCKET or KAMAL_PROXY_DOCKER_SOCKET

kamal-proxy deploy SERVICE --target CONTAINER:PORT \
  [--idle-timeout DURATION] [--idle-wake-timeout DURATION]
  --idle-timeout default: 0 (disabled)
  --idle-wake-timeout default: 30s when idle is enabled
  negative idle durations: rejected
```

The request is held before its body is read, so POST bodies are unchanged. WebSockets and streaming responses remain in flight until their handler returns and therefore prevent sleep; a newly arriving WebSocket/stream waits through wake like an ordinary request.

## Implementation

- `ContainerLifecycle` is the narrow start/stop interface. `DockerClient` is its Unix-socket implementation and can later be replaced by an external lifecycle client without changing the controller.
- The controller uses active/stopping/sleeping/waking transitions. A stopping barrier closes the race between the zero-inflight check and Docker stop; concurrent arrivals then share a single wake result.
- Start success is not enough: held requests wait for the active load balancer to observe a healthy target. Start, readiness, cancellation, and timeout errors release all waiters without consuming request bodies.
- Idle state is serialized in the existing state file. Incomplete stopping/waking transitions recover as sleeping and wake on the next application request. State transitions trigger serialized snapshots.

## Changed files

- `README.md`
- `internal/cmd/deploy.go`
- `internal/cmd/run.go`
- `internal/cmd/util.go`
- `internal/server/config.go`
- `internal/server/docker_client.go`
- `internal/server/docker_client_test.go`
- `internal/server/idle_controller.go`
- `internal/server/idle_controller_test.go`
- `internal/server/load_balancer.go`
- `internal/server/router.go`
- `internal/server/router_test.go`
- `internal/server/service.go`
- `internal/server/service_test.go`
- `internal/server/target.go`
- `internal/server/testing.go`

Local commits: `105c829` (adapted PR #197) and `5d838ea` (race/failure/restart hardening and documentation). Nothing was pushed and no PR was opened.

## Verification

- `go test ./...` — pass
- `go test -race ./internal/server ./internal/cmd` — pass
- `go vet ./...` — pass
- `git diff --check` — pass
- `golangci-lint run` — not run; executable is not installed (`command not found`)

Tests cover no-stop-with-inflight, stop completion versus arriving requests, concurrent wake coalescing, start failure, wake timeout, and persisted incomplete-state recovery.

## Unresolved security and maintainer decisions

- Mounting Docker's socket grants powerful host/container control. Maintainers must decide whether direct socket access is acceptable, whether a restricted socket proxy/external lifecycle service is required, and how container names should be authorized per service.
- The minimal client pins Docker API `v1.41`; maintainers should decide whether API negotiation is required for supported Docker versions.
- Only write targets are stopped. Reader targets and rollout targets remain running; expanding lifecycle scope needs an explicit product decision.
- Multi-container stop is sequential and not transactional. A later stop failure can leave earlier containers stopped; the controller returns active and logs the failure. Desired rollback/reconciliation semantics need a maintainer decision.
- Persisted sleeping state trusts that Docker state still matches the snapshot. The next request issues idempotent starts, but there is no startup reconciliation/list permission.

## Recommended real-Docker integration scenarios

1. Single container: idle stop, POST wake with a large/chunked body, health readiness, exact response verification.
2. Twenty concurrent GET/POST requests against one sleeping service: assert one Docker start and all bodies/results preserved.
3. Long SSE response and WebSocket: assert no stop while connected, then stop after close plus idle duration.
4. Restart kamal-proxy while sleeping and during a forced stopping/waking interruption; assert the next request recovers.
5. Pause/stop/resume and deploy while active, sleeping, and waking; verify no stale container is started and the new target set wins.
6. Docker socket unavailable, permission denied, start/stop HTTP errors, slow Docker response, and container missing/already started/stopped.
7. Container starts but health never succeeds, succeeds near the deadline, or flaps; verify waiter status and retry behavior.
8. Multiple write targets with a partial stop/start failure to decide and validate reconciliation semantics.
