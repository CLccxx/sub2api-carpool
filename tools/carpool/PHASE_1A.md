# Phase 1A Backend Verification and Local Use

This slice adds backend car recruitment only. There are no new frontend pages,
real payment/refund adapters, SMTP messages, server purchases or B-node grants.
All orders are explicitly `test`, and all confirmed members wait for deployment.

## Configuration

Use an isolated local application database, never a production database. Start
the normal application with its existing database/Redis/JWT configuration plus:

```text
CARPOOL_MAIN_ENABLED=true
CARPOOL_MAIN_TEST_ORDERS_ENABLED=true
CARPOOL_NODE_ENABLED=false
```

Both new flags default to false. Main and node mode cannot both be enabled.
The test-orders flag requires main mode. Main enabled + test-orders disabled
keeps catalog, owned order reads/cancellations and administration available, but
rejects new reservations and test confirmation with 404. Completely disabled
main mode (including node B) registers none of these routes.

Startup runs the independent carpool migration stream, including the additive
`002_main_carpool.sql`. The original 001 migration is unchanged. A node running
the shared binary may also have empty main-site tables; no main-site user,
order or member data is copied into them.

The expiry worker starts with a sweep, then runs once per minute, at most 100
cars per batch. Requests also clean expired records under the same car lock.
Reads derive effective expired states from database time even before a sweep.
Shutdown cancels and joins the worker before the existing database cleanup.
Correctness does not depend on Redis notifications or process memory.

## HTTP Contracts

All routes are under `/api/v1`. Use the existing login flow and a Bearer JWT.
User identity is obtained from middleware, never a body parameter. Admin routes
retain the existing admin authentication, compliance guard, rate limit and audit.
This tool does not acknowledge compliance documents on anyone's behalf.

Responses use the existing `{code, message, data}` envelope. Lists include
`items`, `total`, `page`, `page_size` and `server_time`; absent lists are `[]`.
Pagination follows the existing application parser and is capped at 100 rows.
Unknown JSON fields, multiple objects and bodies over 64 KiB are rejected.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/carpool/cars` | Published catalog; optional `status`, `page`, `page_size` |
| GET | `/carpool/cars/:id` | Published car and effective seat counts |
| POST | `/carpool/cars/:id/reservations` | Test reservation; required `Idempotency-Key`; empty body or `{}` |
| GET | `/carpool/orders/:id` | Own order and optional membership |
| POST | `/carpool/orders/:id/cancel` | Cancel own pending order; empty body or `{}` |
| GET | `/carpool/mine` | Own order history with optional membership; optional status filter |
| GET/POST | `/admin/carpool/cars` | List all cars / create draft |
| GET/PATCH | `/admin/carpool/cars/:id` | Read / replace full draft terms |
| POST | `/admin/carpool/cars/:id/publish` | Freeze terms and create fixed seats |
| POST | `/admin/carpool/cars/:id/cancel` | Body `reason` and `confirm_members`; true required if confirmed members exist |
| GET | `/admin/carpool/cars/:id/members` | Paginated members; optional status filter |
| POST | `/admin/carpool/nodes` | Register metadata only: `car_id`, `hostname`, `label` |

Create/PATCH example shape; supply a future deadline appropriate for the local run:

```json
{
  "name": "Local test car",
  "capacity": 2,
  "price_minor": 9900,
  "currency": "CNY",
  "service_days": 30,
  "recruit_until": "2026-09-17T12:00:00+08:00",
  "delivery_hours": 24,
  "refund_policy": "Synthetic test cancellation only; no real money"
}
```

The example is test data, not a real commercial offer. Only CNY minor units are
supported in this slice. Capacity is 2, 3 or 4; name <=160 bytes, refund policy
<=4000 bytes, price_minor 1..1000000000000, service_days 1..3650,
delivery_hours 1..8760, deadline after database time and no more than 10 years
ahead. Published commercial terms cannot be edited. Hostnames are lowercased DNS
names, not URLs; node registration stores no credentials and makes no network call.

Reservation example using environment variables containing local test values:

```sh
curl -sS "$LOCAL_BASE/api/v1/carpool/cars/$CAR_ID/reservations" \
  -H "Authorization: Bearer $USER_JWT" \
  -H "Idempotency-Key: $REQUEST_ID" \
  -H 'Content-Type: application/json' -d '{}'
```

Keys must be 1..128 printable non-space ASCII bytes. Retry the same user/car/key
to get the original order, including its terminal status. Reusing a key for a
different car is a conflict. A new key cannot obtain a second live seat for the
same user in the same car. A reservation expires after 10 minutes or at the
recruitment deadline, whichever is earlier.

## Test Confirmation CLI

There is deliberately **no HTTP payment/confirmation endpoint**.
Build from `backend/`:

```sh
go build -o bin/carpool-test-order ./cmd/carpool-test-order
```

Invoke with a dedicated local DSN and stdin containing one `order_id` object:

```sh
CARPOOL_MAIN_ENABLED=true CARPOOL_MAIN_TEST_ORDERS_ENABLED=true \
CARPOOL_NODE_ENABLED=false CARPOOL_DATABASE_DSN="$LOCAL_TEST_DSN" \
backend/bin/carpool-test-order < /private/path/test-order.json
```

```json
{"order_id":"the-order-id-returned-by-the-local-reservation"}
```

The CLI rejects invalid flags and input before connecting, performs no migration,
confirms only test orders and emits `{"status":"confirmed"}` on success.
Errors are sanitized and nonzero exit status indicates failure. Repetition is
idempotent. Late confirmation fails without stealing a reassigned seat.

Confirmed members stay `waiting_deployment` with null service dates. All held
seats alone do not make a car full. Only all-confirmed seats transition a car to
`awaiting_deployment` and persist one `car_full` event. Cancellation and unfilled
recruitment expiry end test memberships; there is no claimed financial refund.
No operation touches the original balance/subscription fulfillment or inserts
`carpool_node_memberships` grants.

## Verification Commands

From `backend/`, with Docker available for integration tests:

```sh
go test -tags=unit -count=1 ./...
CI=true go test -tags=integration -count=1 ./...
CI=true go test -race -tags=integration -count=1 ./internal/carpool ./cmd/carpool-test-order ./internal/server/routes
golangci-lint run --timeout=10m ./...
CGO_ENABLED=0 go build -tags embed -trimpath -o bin/server-embed ./cmd/server
go build -o bin/carpool-test-order ./cmd/carpool-test-order
```

From the repository root: `make test-frontend` and the phase-0 A/B lab documented
in `README.md`. The API integration harness uses real JWT middleware and
PostgreSQL. Its positive admin-CRUD harness isolates handlers from compliance;
a separate full-route test proves the real compliance guard still returns 423
without acknowledgement. No real acknowledgement is accepted during tests.

Operational evidence and remaining limitations belong in
`docs/carpool/PHASE_1A_RESULTS.md`. Phase 1B owns pages, browser UX and the
desktop/mobile workflow; it is not completed by these backend tests.
