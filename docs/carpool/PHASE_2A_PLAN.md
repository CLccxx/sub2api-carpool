# Phase 2A Manual Enrollment Implementation Plan

> **For agentic workers:** Use executing-plans inline, with test-driven-development for every behavioral change. No subagent dispatch is authorized for this task. Checkboxes record verified work, not intentions.

**Goal:** Deliver administrator-only manual enrollment, immutable receipt records, pre-activation withdrawal/refund tracking, mode isolation and a tested HTTP contract.

**Architecture:** Extend the existing carpool PostgreSQL domain using additive migration 003. Keep manual financial records separate from payment-provider, balance and subscription fulfillment. Reuse administrator authentication, car-first locking, independently persisted expiry work and read-only public feature flags.

**Tech Stack:** Repository-pinned Go 1.27.0, Gin, database/sql, PostgreSQL, testify/testcontainers; existing front-end is unchanged until phase 2B.

## Execution Boundaries

- Work in the current `codex/phase0-node-access` checkout: phase 0/1 prerequisites are uncommitted here and a HEAD-only worktree would omit them. Do not stash, reset, move or commit those existing changes.
- Restore the temporary Go toolchain and run isolated Docker PostgreSQL tests. Do not connect to deployed databases, accept compliance acknowledgements, send money/email or call external payment services.
- This plan ends at the 2A API verification checkpoint. Phase 2B owns Vue pages, browser acceptance and the next A/B UI regression; do not claim the complete manual product is available after 2A alone.
- Keep application dependency manifests and locks unchanged. Compile/test caches live under `/tmp/carpool-*`.

## Files and Responsibilities

| Files | Responsibility |
| --- | --- |
| `backend/internal/carpool/migrations/003_manual_enrollment.sql` | Add mode, nullable manual expiry, receipt/refund/command tables and constraints |
| `backend/internal/carpool/manual_types.go`, `manual_types_test.go` | Inputs, normalization, bounds, administrator receipt and user-safe financial DTOs |
| `backend/internal/carpool/manual_service.go` | Atomic enrollment, withdrawal, refund commands and command idempotency |
| `backend/internal/carpool/manual_queries.go` | Financial projections and administrator order reads/list |
| `backend/internal/carpool/main_{types,service,queries,transactions}.go` | Integrate modes, savepoint rollback, expiry and car cancellation without changing test-mode semantics |
| `backend/internal/carpool/manual_integration_test.go` | Real PostgreSQL lifecycle, concurrency, rollback, mode and privacy evidence |
| `backend/internal/handler/carpool_manual_handler.go`, related tests | Strict bounded input and administrator operation adapters |
| `backend/internal/server/routes/carpool.go`, related tests | Register endpoints only under existing admin security boundaries |
| `backend/internal/server/middleware/audit_log.go`, related tests | Omit sensitive manual request bodies while retaining action metadata |
| Configuration and public settings DTO/service/handler files | Default-off immutable manual feature flag, API/SSR parity |
| `tools/carpool/PHASE_2A.md`, `docs/carpool/PHASE_2A_RESULTS.md` | Reproduction commands, HTTP contract and actual verification evidence |

## Task 1: Environment and Configuration Baseline

- [x] Start Docker Desktop and restore Go 1.27.0 using the official container. Archive downloads timed out; container digest: `sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146`.
- [x] Run baseline: `go test -count=1 ./internal/carpool` and `CI=true go test -tags=integration -count=1 ./internal/carpool` from `backend/`.
- [x] Add a failing configuration test using decoded configuration, so an absent flag fails behaviorally:

```go
var cfg MainConfig
require.NoError(t, json.Unmarshal([]byte(`{"Enabled":true,"TestOrdersEnabled":true,"ManualEnrollmentEnabled":true}`), &cfg))
require.Error(t, cfg.Validate(false))
```

- [x] Implement `ManualEnrollmentEnabled bool` with `mapstructure:"manual_enrollment_enabled"`; manual requires main and is mutually exclusive with test orders. Set the Viper default to false.
- [x] Rerun config/domain unit tests and confirm the previously failing assertions pass.

## Task 2: Migration and Explicit Modes

- [x] Add migration tests proving old node grants/test rows are unchanged and migration is repeatable. New manual creation must expose `enrollment_mode=manual`, fixed full-refund text and no test reservation capability.
- [x] Run the tests and capture the missing behavior before adding migration 003.
- [x] Add `enrollment_mode` defaulting old cars to test, extend order mode, permit null `expires_at` only for manual orders and add needs_review. Keep 001/002 bytes unchanged.
- [x] Add one-to-one receipt/refund tables and actor/action/key command uniqueness; use integer minor units and permanent channel/reference uniqueness.
- [x] Set mode on the server during creation; reject writes to incompatible modes. Public reads hide test cars when manual enrollment is active. Add Go DTOs without exposing reference or notes to members.
- [x] Rerun migration tests and existing test-order integration tests; update only the migration-count expectation from 2 to 3.

## Task 3: Manual Enrollment Transaction

- [x] Write lifecycle/negative tests for full-price receipt confirmation, disabled/unknown user, duplicate reference, same-key replay and last-seat contention.
- [x] Expose the domain command:

```go
func (s *MainService) EnrollManual(ctx context.Context, actor int64, carID, key string, input ManualEnrollmentInput) (ManualOrder, error)
```

- [x] Validate normalized input and active actor/target, lock the vehicle first, refresh database time and expiry, then isolate command writes with a savepoint. On a business rejection roll back to that savepoint; database errors roll back the complete transaction.
- [x] Persist confirmed order/seat/member/receipt/audit and command result atomically. Fill the car exactly once at capacity. Replay returns the same record's current state and never resurrects a member.
- [x] Verify 20 concurrent last-seat attempts yield one success; duplicate actor/key and reference never create extra records; injected SQL failures leave no partial command data or B grants.

## Task 4: Withdrawal, Closure and Manual Refund

- [x] Write lifecycle tests first for recruitment withdrawal, full-car needs_review, full cancellation and close-before-sweep financial projection; supplement with lock-wait and worker restart regressions.
- [x] Implement administrator commands:

```go
func (s *MainService) WithdrawManual(ctx context.Context, actor int64, orderID, key string, input ManualWithdrawalInput) (ManualOrder, error)
func (s *MainService) RefundManual(ctx context.Context, actor int64, orderID, key string, input ManualRefundInput) (ManualOrder, error)
```

- [x] Preserve receipt records when ending members. Upsert exactly one full-amount pending refund per cancelled manual order in the same car transaction; never mark a refund complete during expiry/cancellation.
- [x] Confirming offline refund requires explicit acknowledgement, matching amount, nonfuture timestamp and pending state. Repeat keys return current state; changed payload conflicts. Pending same-user/same-car refund blocks reenrollment.
- [x] Keep reads and refund operations available when manual additions are disabled. Full-car withdrawal enters needs_review and never reopens recruitment. Test-mode closure remains nonfinancial.
- [x] Rerun lifecycle and race tests, including readonly-before-sweep and worker restart.

## Task 5: Administrator HTTP and Privacy

- [x] Add HTTP/auth/compliance/audit tests before route wiring, then strict-body regression coverage for these endpoints:

```text
POST /api/v1/admin/carpool/cars/:id/members
GET  /api/v1/admin/carpool/orders
GET  /api/v1/admin/carpool/orders/:id
POST /api/v1/admin/carpool/orders/:id/withdraw
POST /api/v1/admin/carpool/orders/:id/refund
```

- [x] Wire commands through the existing administrator group. Obtain actor from auth context only; reject unknown JSON fields, missing keys, oversized or multiple JSON bodies.
- [x] Add administrator order listing with optional car/status/refund filters, including ended vehicles. User order reads return only own amount/channel/times/refund status; admin reads additionally expose receipt reference, notes and confirming actor.
- [x] Omit full sensitive request bodies for new mutation routes from general audit capture; retain action, route, actor and status. Ordinary users cannot inspect administrator records or call commands.
- [x] Use real PostgreSQL/JWT HTTP tests for success and 401/403/404/409; retain production 423 compliance interception without accepting declarations.

## Task 6: Public Flag and Verification

- [x] Add failing API/SSR parity tests for `carpool_manual_enrollment_enabled`; it must be present as a boolean, default false and never sourced from mutable settings.
- [x] Wire Viper, public service payload, DTO and SSR injection. Fail closed under node mode or conflicting configuration.
- [x] Run affected unit tests, domain/HTTP integration with race, CLI tests, backend build and full lint. Container lint passed with reduced concurrency after the initial exit 137 and one QF1008 correction; details in `PHASE_2A_RESULTS.md`.
- [x] Check `git diff --check`, review changed source for money/identity leaks and review the domain tests against every phase-2A design requirement.
- [x] Write reproduction instructions and results, update HANDOFF and the approved design's progress. Keep prior phase changes uncommitted; no remote push.

## Verification Commands

Commands run from `backend/` inside the official Go container. Host caches `/tmp/carpool-go-build` and `/tmp/carpool-go-mod` are mounted into the container; no host Go installation or dependency-lock update is needed. Full container commands belong in `tools/carpool/PHASE_2A.md`:

```sh
go test -tags=unit -count=1 ./internal/carpool ./internal/config ./internal/handler ./internal/service ./internal/server/middleware ./internal/server/routes
CI=true go test -race -tags=integration -count=1 ./internal/carpool ./internal/server/routes ./cmd/carpool-test-order
go build ./cmd/server ./cmd/carpool-test-order ./cmd/carpool-membership
```

The integration browser harness requires frontend assets and is phase-2B acceptance, not claimed by the API-focused checks above. All expected successes must be backed by fresh command results.
