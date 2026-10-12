# Phase 3A: Node Control Connections

This describes the connection slice, not a production rollout or service activation. Phase 3B has since added members/keys/grants and 3C the user interface; see `PHASE_3C.md` and `docs/carpool/PHASE_3_RESULTS.md` for final verification. The phase-2 lobby layout remains unchanged.

## Configuration Contract

Main A:

```text
CARPOOL_MAIN_ENABLED=true
CARPOOL_CONTROL_ENABLED=true
CARPOOL_CONTROL_ENCRYPTION_KEY=<independent base64 encoding of 32 random bytes>
CARPOOL_CONTROL_ALLOWED_ORIGINS=https://node.example,https://another.example:8443
```

The example domains are illustrative, not configured or contacted by this task. The encryption root must be persistent and separately backed up; do not reuse JWT or TOTP keys. An absent/invalid/shared root prevents startup with control enabled. Replacing the root without a ciphertext migration prevents decryption; it is not administrator-key rotation. Default control is off.

Node B uses `CARPOOL_NODE_ENABLED=true`, `CARPOOL_NODE_AUTHORIZATION_MODE=managed`, canonical `CARPOOL_NODE_MAIN_INSTANCE_ID`, `CARPOOL_NODE_NODE_ID`, `CARPOOL_NODE_CAR_ID` UUIDs and a positive existing `CARPOOL_NODE_GROUP_ID`. Use standard run mode and disable OIDC for managed nodes. OIDC remains the old default node authorization mode. A database with old OIDC grants cannot silently become a managed node.

The ordinary global URL-allowlist switch does not disable this control client's checks. Production loopback/private/metadata addresses are not permitted even when a hostname is explicitly allowed. Test injection exists only in test code, not deployment flags.

## API Sequence

All endpoints below are administrator-only. Do not paste real credentials into chat or store them in logs/history.

1. Register the vehicle's node with existing `POST /api/v1/admin/carpool/nodes`; retain its node and car UUIDs.
2. Read A's `GET /api/v1/admin/carpool/control` for the main instance UUID. Configure B with this main/node/car identity.
3. Read B's `GET /api/v1/admin/carpool-control/v1/info` using B's own administrator Key, supplied as `x-api-key`; it returns its persisted node instance UUID and protocol version.
4. In A, `PUT /api/v1/admin/carpool/nodes/:id/connection` accepts `origin`, `node_instance_id`, `admin_key`, `expected_generation` and optional `disabled`. A new connection uses generation 0; later saves use the generation from the last GET. A blank key preserves/re-encrypts an existing key; it cannot initialize one.
5. Call A's `POST /api/v1/admin/carpool/nodes/:id/connection/check` with `{}`. Read current state at `GET /api/v1/admin/carpool/cars/:id/connection`. `ready` means connection/identity verification only, never service activation.

A stores B's credential in the node connection, not in A's global administrator-key setting. Save/check respects the existing step-up policy. Each server retains its own administrator key. Full B administrator authority remains a production security-review concern.

For uncertain saves, read the current generation before deciding on a new save; stale generation returns conflict. For uncertain checks, fetch current status. Origin and expected instance can be corrected before first verification but are pinned afterwards; moving an established vehicle to a new database requires a separate migration design.

## Verification

Use the Docker/cache wrapper in `PHASE_2A.md`, with `GIN_MODE=release` to keep route test output compact.

```sh
go test -mod=readonly -race ./internal/carpool ./internal/config
go test -mod=readonly -race ./internal/handler ./internal/server/routes ./internal/server/middleware -run TestCarpoolControl
go test -p 2 -mod=readonly -race -tags=integration ./internal/carpool ./internal/config ./internal/server/routes ./cmd/carpool-test-order
go build -p 2 -mod=readonly ./cmd/server ./cmd/carpool-test-order ./cmd/carpool-membership
golangci-lint run --concurrency=1 --timeout=10m ./...
```

Integration commands need the local Docker socket for disposable PostgreSQL and the documented host override on macOS. The browser harness remains opt-in and is not implied by these commands. Actual results and remaining work are in `docs/carpool/PHASE_3A_RESULTS.md`.
