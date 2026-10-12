# Phase 2A: Manual Enrollment API

This backend slice records an administrator's confirmation of an off-platform
receipt. It does not contact Alipay or WeChat, verify a transfer, issue a refund,
change balances/subscriptions, or activate a node. Use synthetic local data only.
The Vue forms and browser acceptance belong to phase 2B.

## Configuration

```yaml
carpool_main:
  enabled: true
  test_orders_enabled: false
  manual_enrollment_enabled: true
carpool_node:
  enabled: false
```

Equivalent environment variables:

```text
CARPOOL_MAIN_ENABLED=true
CARPOOL_MAIN_TEST_ORDERS_ENABLED=false
CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED=true
CARPOOL_NODE_ENABLED=false
```

Manual enrollment defaults to false. Manual/test enrollment flags are mutually
exclusive; main/node modes remain mutually exclusive. Enabling main runs the
independent, checksum-checked carpool migrations including additive migration
003. Back up production before any separately authorized rollout; this document
does not authorize a rollout. Do not alter migrations 001/002.

New cars get a server-selected `enrollment_mode`. Manual cars always use the
server's fixed full-refund policy. Existing cars/orders remain test data.
With manual mode enabled, user catalog/detail/history exclude test data;
administrators can still inspect historical test cars. Turning manual additions
off preserves historical reads, withdrawal, cancellation, refund registration
and expiry work while main remains enabled. Both enrollment flags off retains
the earlier read-only catalog behavior; the UI must use each record's mode, not
infer it solely from a global flag.

The read-only boolean `carpool_manual_enrollment_enabled` appears in public
settings API and SSR injection. It is not an editable database setting.
Do not enable the manual flag for end users before phase 2B pages are verified.

## HTTP Contract

All paths below use `/api/v1` as prefix. Existing administrator authentication,
compliance and audit middleware remain in place. Production compliance returns
423 until its own requirements are satisfied; tests do not accept declarations.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/admin/carpool/cars/:id/members` | Confirm full off-platform receipt and add a registered user |
| GET | `/admin/carpool/orders` | Manual orders, including cancelled orders and pending refunds |
| GET | `/admin/carpool/orders/:id` | Manual order and private receipt/refund details |
| POST | `/admin/carpool/orders/:id/withdraw` | Withdraw member and queue full manual refund |
| POST | `/admin/carpool/orders/:id/refund` | Record that the administrator has already refunded off-platform |

Existing `/admin/users` supplies user search. Submit its stable `user_id`, never
a nickname. Existing car CRUD/publish/cancel/member routes remain available.
Each mutation above requires `Idempotency-Key`: 1-128 printable non-space ASCII
characters. Reuse the original key and identical normalized body after an
unknown response. Same actor/action/key with different content returns 409;
replay returns the original order's current state, including cancellation.

Enrollment body (fictional example; replace the timestamp with an actual
nonfuture test timestamp):

```json
{
  "user_id": 2,
  "amount_minor": 9900,
  "channel": "alipay",
  "received_at": "2026-09-18T09:00:00+08:00",
  "reference": "synthetic-transfer-001",
  "note": "Synthetic local receipt; no money moved",
  "confirm_received": true
}
```

`channel` is `alipay` or `wechat`. Amount is integer CNY minor units and must
equal the frozen seat price, in 1..1000000000000. The server rejects disabled or
unknown users, duplicate live seats, pending refunds for the same user/car,
full/closed/draft/test cars and future receipt times. `reference` and `note` are
optional; surrounding whitespace is trimmed. References must be at most 128
printable non-space ASCII characters; notes at most 1000 bytes. A reference is
unique per channel permanently, even after refund. Empty references provide no
external transaction deduplication. Timestamps use RFC3339 with timezone and are
normalized to UTC microseconds. Recording time and actor are server controlled.

Withdrawal body:

```json
{"reason":"Synthetic customer withdrawal","confirm":true}
```

Reason is required, trimmed and at most 2000 bytes. Withdrawal preserves the
receipt, cancels the order/member and releases the seat. A full car moves to
`needs_review`, keeping its original full/delivery times and rejecting new
enrollment. There is no automatic replacement. Recruiting cars can continue
within their original deadline, but reenrolling that same user requires their
previous refund to be completed first.

Refund body:

```json
{
  "amount_minor": 9900,
  "refunded_at": "2026-09-18T10:00:00+08:00",
  "reference": "synthetic-refund-001",
  "confirm_refunded": true
}
```

Only full refunds of cancelled manual orders are supported. Refund time must
not precede receipt time or be in the future. Reference is optional, with the
same character limits as receipts. Confirmed refund facts cannot be overwritten.
Cancellation/expiry generates `pending`; only this explicit confirmation changes
it to `refunded`. No money moves through any endpoint.

List query parameters: `page` (default 1), `page_size` (default 20, maximum 100),
optional `car_id`, `status=confirmed|cancelled`, and
`refund_status=none|pending|refunded`. Invalid filters are rejected.
The current bounded list implementation performs detail queries per result;
production-volume latency has not been benchmarked.

Bodies must be one JSON object, at most 64 KiB. Unknown fields, null/arrays,
trailing data, missing confirmation and client-supplied actor fields are rejected.
Expected status families: 400 invalid input, 401 unauthenticated, 403 non-admin,
404 missing/other-user/disabled feature, 409 state/mode/idempotency conflicts,
413 oversized body, 423 compliance gate, 503 unavailable database.

## Privacy and Lifecycle

User `/carpool/orders/:id` and `/carpool/mine` expose only that user's payment
amount/currency/channel/times and refund state, never receipt references,
internal notes, refund reasons or administrator identities. Admin order/member
views include those details. General request audit omits the complete sensitive
body for enrollment, withdrawal, refund and car cancellation; domain events
retain action/actor/resource IDs without receipt text.

Manual members are `waiting_deployment` with null service dates. At capacity,
cars become `awaiting_deployment`, not active. Users cannot self-reserve or
cancel manual membership through legacy test endpoints. The test-order CLI
rejects manual-enabled configuration before connecting and checks order mode
in the transaction.

Before an expiry sweep, read projections already show closed recruitment and
pending refunds. Sweep/restart persists missing refund tasks idempotently.
Car-first locks serialize enrollment/withdrawal/closure. A savepoint rolls back
business-command writes on rejection, while preceding expiry cleanup can commit;
SQL failures roll back the whole transaction.

## Reproduce Verification

On a host with the repository-pinned Go 1.27.0 and Docker, run from `backend/`:

```sh
go test -p 2 -mod=readonly -tags=unit -count=1 ./internal/carpool ./internal/config ./internal/handler ./internal/service ./internal/server/middleware ./internal/server/routes ./cmd/carpool-test-order ./cmd/carpool-membership
CI=true go test -p 2 -mod=readonly -race -tags=integration -count=1 ./internal/carpool ./internal/server/routes ./cmd/carpool-test-order
go build -p 2 -mod=readonly ./cmd/server ./cmd/carpool-test-order ./cmd/carpool-membership
golangci-lint run --timeout=10m ./...
```

This run used official `golang:1.27.0`, because the host had no Go and official
archive downloads timed out. On this macOS workspace, the integration equivalent
is the following. Create the two empty cache directories first if absent, and
use the Docker socket location for the host in question.

```sh
docker run --rm \
  --mount type=bind,src=/Users/cl/Desktop/OwnProject/sub2api-carpool,dst=/workspace,readonly \
  --mount type=bind,src=/tmp/carpool-go-mod,dst=/go/pkg/mod \
  --mount type=bind,src=/tmp/carpool-go-build,dst=/root/.cache/go-build \
  --mount type=bind,src=/Users/cl/.docker/run/docker.sock,dst=/var/run/docker.sock \
  -w /workspace/backend -e GOTOOLCHAIN=local -e GOMAXPROCS=4 \
  -e CI=true -e TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal \
  golang:1.27.0 go test -p 2 -mod=readonly -race -tags=integration -count=1 \
  ./internal/carpool ./internal/server/routes ./cmd/carpool-test-order
```

Unit tests/build use the same Go container without the Docker socket or
testcontainers variables. The linter uses `golangci/golangci-lint:v2.13.0`, the CI
version. The successful full lint run used `GOMAXPROCS=2`, `GOMEMLIMIT=2GiB`,
`GOFLAGS=-p=1` and `--concurrency=1 --timeout=10m` after the first container exited
137 without results. PostgreSQL tests create/destroy isolated `postgres:18.1-alpine3.23`
containers. A Docker socket grants broad local daemon access; use it only in a
trusted local test environment, never expose it to an application container.

`TestCarpoolBrowser` intentionally skips unless `CARPOOL_BROWSER_TEST=1`; phase
2A did not run that browser harness, A/B browser regression, frontend builds or
production deployment. See `docs/carpool/PHASE_2A_RESULTS.md` for actual results.
