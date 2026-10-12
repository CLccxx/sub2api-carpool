# Phase 4D End-To-End Delivery Plan

> Executed inline with `executing-plans`. 4A, 4B and 4C passed before 4D. Local 4D verification, requirement audit and preview delivery completed on 2026-09-23; see `PHASE_4D_RESULTS.md`.

**Goal:** Verify the complete native subscription delivery and model-request lifecycle, document a reproducible compatible deployment, and provide a current local preview.

**Architecture:** Reuse A's durable membership/connection operations and independent B/C production processes. Model traffic never goes through A. Native PostgreSQL billing and real Redis invalidation remain authoritative; test fixtures supply only synthetic upstream responses and controlled time transitions.

**Tech Stack:** Existing Go integration harness, PostgreSQL/Redis Testcontainers, embedded production server, Vue/Playwright, Docker Compose configuration checks.

No commit, push, public upload, purchase, production deployment, real OAuth authorization or acceptance of legal declarations is authorized. Existing broader service-race failures remain explicitly separate from affected-path results.

## 1. Native Model Lifecycle

Files: add `backend/internal/carpool/native_model_integration_test.go`; extend only the test return value in `control_model_integration_test.go` to expose its isolated Redis client for native invalidation.

- [x] Create a four-member car and an independent two-member car with manual receipts, through the actual main service. Use the actual preparation/activation methods rather than test SQL to grant service:

```go
_, err = store.PrepareNativeCar(ctx, administratorID, car.ID, accountID)
require.NoError(t, err)
// Drain the persisted preparation queue and require readiness before activation.
_, err = store.ActivateNativeCar(ctx, administratorID, car.ID, true)
require.NoError(t, err)
```

- [x] Assert prepared-only members cannot create usable keys; initialization creates the required native groups/subscriptions, preserves zero balances, and starts at 450/900 respectively.
- [x] Launch the current production B/C binary against the existing local completion stub. The selected synthetic OAuth row is not used for external network traffic; a local API-key transport account supplies model responses. This validates native gateway/subscription accounting, not real OAuth refresh or upstream reset behavior.
- [x] Build a second native controller over the same B database with the production process's Redis connection and real API-key/subscription cache adapters. This permits quota changes to exercise genuine cross-process invalidation without bypassing the production compliance guard.
- [x] Send direct B/C requests; require native usage records and positive per-user weekly subscription charges while balance remains zero. A's member reads must equal B's stored records and values. Different member/car keys must remain isolated.
- [x] Apply a valid current-cycle estimate of 2000; require 475 and unchanged usage. Apply a tiny positive limit below recorded usage; require the production B request to return `429 USAGE_LIMIT_EXCEEDED` without contacting the upstream. Restore a valid limit and require successful requests.
- [x] Seed only previous-cycle timestamps, then reconcile a fresh next-cycle observation. Require all native weekly usage to reset, histories and subscription expiry to survive, and replay after new model charges to retain those charges.
- [x] Verify disabled/deleted keys, native expiry and revoked memberships reject new model requests. Keep existing in-flight-stream coverage from the historical production model test.
- [x] Close A's `sql.DB` pool after management assertions (not PostgreSQL or an A process). Force the old B lease timestamp into the past; valid native model requests must still work. Native expiry must still reject requests while A remains unavailable.

Build from `backend/`, mounting the source at its exact host path when cross-compiling:

```sh
go test -p=1 -mod=readonly -c -tags=integration -o /tmp/carpool-4d-bin/carpool-control.test ./internal/carpool
go build -p=1 -mod=readonly -tags=embed -o /tmp/carpool-4d-bin/server ./cmd/server
CARPOOL_MODEL_BINARY=/tmp/carpool-4d-bin/server CI=true GIN_MODE=release \
DOCKER_HOST=unix:///Users/cl/.docker/run/docker.sock \
/tmp/carpool-4d-bin/carpool-control.test -test.v \
  -test.run '^Test(NativeSubscriptionProductionLifecycle|ControlProductionModelNodes)$' -test.timeout=12m
```

New assertions may uncover existing implementation defects. For any such defect, retain the failing evidence, implement the smallest fix, and rerun the original failing path plus affected regressions. Do not weaken assertions or replace native billing with fixtures.

## 2. Deployment And Recovery Instructions

Files: `tools/carpool/PHASE_4.md`, scoped `deploy/carpool/` templates if needed, and verification tests for configuration requirements.

- [x] Document the compatible customized B requirement and immutable local image/build identity. Do not claim the current uncommitted worktree is available from upstream or a public release.
- [x] Supply separate A/B settings using the existing configuration fields: manual enrollment on A, managed mode and auto-group ID zero on B, independent databases/Redis/secrets, HTTPS origin allowlist and stable main/node/car IDs.
- [x] Describe manual server provisioning, administrator-controlled OAuth and compliance setup, connection check, account selection, initialize, explicit activation, then member key creation. Explain no password synchronization and no online payments.
- [x] Document native source freshness, `(T-100)/N`, fallback `1800/N`, hourly limits, minute cycle checks, bounded timestamp corrections and same-cycle manual synchronization. Include unavailable-source/cache-retry behavior without an A request pause.
- [x] Validate template parsing and flag names locally without starting production services. Explain backups, encrypted-key recovery, pinned-version upgrades and rollback boundaries.

## 3. Final Verification And Preview

- [x] Run affected phases 1-3 integration/race, latest native lifecycle and historical model request tests, and manual/legacy/OIDC browser regressions using rebuilt binaries.
- [x] Run frontend full tests, lint/typecheck/build, backend default and integration-tag lint. Inspect current desktop/mobile screenshots rather than relying only on assertions.
- [x] Write `PHASE_4D_RESULTS.md` and a phase-4 requirement/evidence audit. Each requirement must point to a real assertion or inspected artifact. Keep real OAuth, SMTP delivery, production TLS/DNS/independent egress and deployment explicitly unverified.
- [x] Start only an isolated loopback preview with synthetic credentials. Provide member/admin entry links, state its lifetime and cleanup method, and keep secrets out of public artifacts.
- [x] Update `HANDOFF.md` and `PHASE_4_EXECUTION_PLAN.md`. Mark the goal complete only when the entire approved local development scope is proven, not merely because a test subset is green.
