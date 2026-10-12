# Phase 3C Browser and Model Acceptance

All commands use synthetic local fixtures. No activation/preview endpoint is
compiled into the production server. The two new integration tests live in
`backend/internal/carpool` with the `integration` build tag.

## Preparation

Build the frontend (`pnpm build` in `frontend/`) and host-platform binaries:

```sh
go test -mod=readonly -c -tags=integration -o /tmp/carpool-3c-bin/carpool-control.test ./internal/carpool
go test -mod=readonly -c -tags=integration -o /tmp/carpool-3c-bin/carpool-browser.test ./internal/server/routes
go build -mod=readonly -tags=embed -o /tmp/carpool-3c-bin/ ./cmd/server ./cmd/carpool-test-order ./cmd/carpool-membership
```

Run these from `backend/`. When using Docker on Apple Silicon, cross-compile with
`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0`, mount the source at its exact host path
(the browser harness uses `runtime.Caller`), and mount the output directory and
existing Go caches. The current host has no Go installation; see `PHASE_2A.md` for
the Docker wrapper. Browser scripts use the existing `tools/carpool/node_modules`,
Node and local Chrome. Testcontainers requires the Docker socket.

## Native Acceptance

```sh
CARPOOL_CONTROL_BROWSER=1 \
CARPOOL_MODEL_BINARY=/tmp/carpool-3c-bin/server \
CI=true GIN_MODE=release DOCKER_HOST=unix:///Users/cl/.docker/run/docker.sock \
/tmp/carpool-3c-bin/carpool-control.test \
  -test.v -test.run '^TestControl(BrowserNative|ProductionModelNodes|NativeHTTPMainToTwoNodes)$' -test.timeout=10m
```

- `TestControlBrowserNative` starts real A/B/C database and TLS control fixtures,
  then runs `control-browser.mjs` against the built Vue app. It tests actual A
  email/password login, prepared-only access, connection saves/checks, committed
  key-response loss and reload recovery, key changes, per-car isolation, scoped
  usage, node outage, desktop/mobile localization and overflow.
- `TestControlProductionModelNodes` launches the unmodified production server
  binary for B and C with independent Redis/PostgreSQL and a local model upstream.
  Management uses the native test control router; real model requests go directly
  to the production node processes. Native usage is then queried by A. No model
  traffic passes through A. Test SQL supplies the explicit active/billing fixture
  only after asserting that preparation created zero balance and no usable key.
- `TestControlNativeHTTPMainToTwoNodes` retains the broader native HTTP contract
  fixture. Its directly inserted usage rows are distinct from the production
  model test's genuine gateway-generated usage.

The positive control fixtures do not accept compliance declarations. Their
loopback transport override is exported only from `export_test.go`, not from a
production runtime flag. Run production auth/compliance/step-up tests separately.
The new browser fixture does not install a refresh-token cache; access-token
fallback log messages are expected. It never issues a node member web session.

## Historical Regression

Follow `PHASE_2B.md` with the current `/tmp/carpool-3c-bin/` binaries for the 12
manual, 18 legacy UI and 16 OIDC A/B groups. Keep the original baseline binary
separate; do not build it from the modified worktree.

## Local Preview

```sh
CARPOOL_CONTROL_PREVIEW=1 CI=true GIN_MODE=release \
DOCKER_HOST=unix:///Users/cl/.docker/run/docker.sock \
/tmp/carpool-3c-bin/carpool-control.test \
  -test.v -test.run '^TestControlBrowserNative$' -test.timeout=9h
```

This explicitly opted-in test binary prints loopback-only `/__preview/admin` and
`/__preview/member` links. They create synthetic local sessions, not production
logins. Use separate browser profiles for simultaneous roles. Cars remain
unactivated; no model/billing fixture is applied. Reopening the role link renews
its short-lived test session. SIGINT stops the preview and cleans its containers;
otherwise it stops after eight hours. Retained logs/artifacts are private local
files and must not be published.
