# Phase 1A Carpool Model and Transaction API Implementation Plan

> Execution: follow test-driven-development, subagent-driven-development for the bounded HTTP/config/CLI task, and verification-before-completion. Work in the existing checkout without reverting or committing earlier phase-0 changes. No real payment, production deployment, or remote push.

**Goal:** Deliver the approved phase-1 server slice: durable cars, seats, test orders, waiting members, node registration, events, expiration and protected HTTP APIs.

**Architecture:** Keep domain and PostgreSQL transactions in `internal/carpool`; use existing Gin authentication/guards/audit and Wire providers. Car-row locks serialize every mutation; unique constraints enforce ownership and idempotency. No Redis correctness dependency, no payment fulfillment, no B grants.

**Tech Stack:** Existing Go/PostgreSQL/Gin/Wire/testcontainers; no new application dependencies.

**Status (2026-09-16):** Tasks 1-4 complete for the local phase-1A backend scope. Evidence, final CORS fix, test-run ordering and limitations are in `PHASE_1A_RESULTS.md`. Phase 1B and P1-16 remain pending; no commits or remote pushes.

## Shared Contract

Core files owned locally: `backend/internal/carpool/main_types.go`, `main_service.go`, `main_queries.go`, `main_transactions.go`, `main_integration_test.go`, `main_types_test.go`, `migrations/002_main_carpool.sql` and the existing migration integration test as needed.

HTTP/config/CLI task owns `backend/internal/config/config.go` plus its new tests, `backend/internal/handler/carpool_handler.go` plus tests, handler aggregates/providers, `backend/internal/server/routes/carpool.go` plus tests and registration calls, `backend/internal/service/carpool_main_provider.go` plus worker tests, service Wire provider registration, server cleanup wiring/generated output, and `backend/cmd/carpool-test-order/`.

```go
type MainConfig struct { Enabled bool; TestOrdersEnabled bool }
// mapstructure: enabled, test_orders_enabled
func (MainConfig) Validate(nodeEnabled bool) error
type PageQuery struct { Page, PageSize int; Status string }
type CarInput struct {
    Name string; Capacity int; PriceMinor int64; Currency string
    ServiceDays int; RecruitUntil time.Time; DeliveryHours int; RefundPolicy string
}
// JSON uses snake_case; IDs are opaque UUID strings.
type NodeInput struct { CarID, Hostname, Label string }
func NewMainService(db *sql.DB, cfg MainConfig) *MainService
func (*MainService) Enabled() bool
func (*MainService) TestOrdersEnabled() bool
func (*MainService) CreateCar(context.Context, int64, CarInput) (Car, error)
func (*MainService) UpdateCar(context.Context, int64, string, CarInput) (Car, error)
func (*MainService) PublishCar(context.Context, int64, string) (Car, error)
func (*MainService) CancelCar(context.Context, int64, string, string, bool) (Car, error)
func (*MainService) ListCars(context.Context, bool, PageQuery) (CarPage, error)
func (*MainService) GetCar(context.Context, string, bool) (Car, error)
func (*MainService) Reserve(context.Context, int64, string, string) (Order, error)
func (*MainService) GetOrder(context.Context, int64, string) (Order, error)
func (*MainService) CancelOrder(context.Context, int64, string) (Order, error)
func (*MainService) Mine(context.Context, int64, PageQuery) (OrderPage, error)
func (*MainService) ListMembers(context.Context, string, PageQuery) (MemberPage, error)
func (*MainService) RegisterNode(context.Context, int64, NodeInput) (Node, error)
func (*MainService) ConfirmTestOrder(context.Context, string) (Order, error)
func (*MainService) Sweep(context.Context) (int, error)
```

Car embeds CarInput and includes id/status/version/created_at/published_at/full_at/delivery_due/confirmed_count/held_count/available_count/reservable/server_time. No creator ID or private node data in public Car responses. Order contains id/car_id/user_id/seat_no/mode/status/snapshot/expires_at/confirmed_at/created_at and optional membership. Membership contains id/car_id/user_id/seat_no/order_id/status/created_at and null service_start/service_end. Node contains id/car_id/hostname/label/status/created_at. Pages use items/total/page/page_size/server_time. Nil collections serialize as empty arrays.

## Task 1: Policy and Database Foundation (Local)

- [x] Add failing validation and real-PostgreSQL tests before implementation. Cover capacities 2/3/4 and invalid 1/5, required metadata, disabled/conflicting flags, migration idempotency/checksum, user/seat/owner constraints. Run `go test -tags=integration -count=1 ./internal/carpool` and record the initial failure.
- [x] Add types, strict CNY minor-unit validation, pages and domain errors. Bound text/input sizes, positive durations and safe pagination; deadlines checked against DB time, not browser time.
- [x] Add `002_main_carpool.sql` without changing 001. Cars/seats/orders/memberships/nodes/events have restrictive user FKs, opaque IDs, partial unique active seat/member constraints, compound ownership FKs and test-only status/mode constraints. Existing migrations test must expect the added migration and provide compatible users fixture.
- [x] Run unit/policy tests and PostgreSQL foundation tests to green.

## Task 2: Transactions and Reads (Local)

- [x] Write failing scenarios before methods: car publish freezes terms; test reservation, same/different-key repeat, same-user same-car vs different-car; all-held is not full; confirmation is idempotent; cancellation requires reason and explicit acknowledgement for confirmed members.
- [x] Implement Create/Update/Publish, public/admin list/detail, Reserve, owned order reads/cancel, Mine, member lists and node metadata registration. No remote requests. Strict ownership is server-side and unknown bodies rejected by HTTP adapter.
- [x] Implement ConfirmTestOrder and Sweep. Lock car first, use `clock_timestamp()` after lock, clean expired holds, atomically confirm seat/order/member/full event. Expired request rejection must not roll back legitimate expiry cleanup. Transaction infrastructure failures roll back every write.
- [x] Add real DB races: 20 users last seat, concurrent idempotent reservation/confirmation, confirm vs expiry/cancel, repeat sweep/restart; inject DB failure after writes and prove rollback. Confirm no users.balance/subscriptions/node-memberships writes and no service activation.
- [x] Verify `CI=true go test -tags=integration -count=1 ./internal/carpool`.

## Task 3: HTTP, Configuration, Lifecycle and CLI (Delegated)

- [x] First add config/handler/CLI tests that fail without implementation. Main default off, conflicts with node mode, test-orders requires main mode. Main on + tests off allows admin/catalog reads and car administration but refuses reservation/confirmation; clarify this narrower boundary in results.
- [x] Add config field `CarpoolMain carpool.MainConfig` and viper defaults `carpool_main.enabled`, `carpool_main.test_orders_enabled`.
- [x] Add handler and routing matching PHASE_1_DESIGN section 7, inside existing user/admin middleware groups; register nothing when main disabled or node enabled. User ID comes from auth context. Strict single JSON object, reject unknown fields and malformed/oversized bodies. No confirm-payment HTTP endpoint. Admin cancellation input is reason + confirm_members. Node input accepts only car_id/hostname/label.
- [x] Add provider that migrates only when main enabled, constructs MainService; expiry worker starts with startup sweep, runs bounded periodic batches and stops before DB shutdown. Log errors without credentials. Add fake-sweeper lifecycle tests.
- [x] Add local CLI `carpool-test-order`: explicit `CARPOOL_MAIN_ENABLED=true`, `CARPOOL_MAIN_TEST_ORDERS_ENABLED=true`, reject node mode, `CARPOOL_DATABASE_DSN`, stdin `{ "order_id": "..." }`, one object/unknown fields rejected. No migration/connection without valid flags and input. Only ConfirmTestOrder is exposed; no web listener.
- [x] Regenerate Wire after integration and run relevant tests and builds.

## Task 4: Integration, Review and Handoff (Local)

- [x] Review delegated spec compliance then code quality. Fix findings and re-run tests, do not stage or commit unrelated changes.
- [x] Run `go test -tags=unit -count=1 ./...`, `CI=true go test -tags=integration -count=1 ./...`, `golangci-lint run --timeout=10m ./...`, embedded backend/CLI builds, and frontend critical tests. Re-run local A/B lab to guard prior node behavior against migration/DI changes.
- [x] Record P1-01 through P1-15 evidence separately from future P1-16 UI coverage. Document config, curl/CLI contracts, commands, pass/fail/skip states, limitations and cleanup. Update HANDOFF/IMPLEMENTATION_PLAN. Keep real payments, SMTP, deployment, sync, UI, commits and pushes out of this run.
