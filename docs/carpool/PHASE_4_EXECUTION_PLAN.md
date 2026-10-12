# Phase 4 Execution And Verification

**Goal:** Deliver the approved 4A-D scope sequentially, verifying each slice before the next.

**Architecture:** Reuse the existing A carpool portal, member mappings and B native billing. Do not copy passwords, create a second usage ledger or pause model requests during synchronization. Native subscription protection must replace the extra lease before removing that lease.

**Tech Stack:** Existing Go/PostgreSQL/Redis backend, Vue/TypeScript frontend and local Docker/browser test harnesses.

Local phases 4A-D completed sequentially on 2026-09-23; see each RESULTS document and 4D's evidence audit. Work remains in the current dedicated branch because phases 0-4 are uncommitted there. No reset, commit, push, resource purchase or production deployment is authorized by this local implementation plan.

## 4A: Price And Occupancy

- [x] RED: test CNY total-price input, integer rounding for 2/3/4 seats, rejection of invalid totals, legacy price preservation and confirmed-seat display.
- [x] GREEN: add a nullable `total_price_minor` through an additive migration; derive `price_minor` on the server. Existing cars and order snapshots remain unchanged.
- [x] GREEN: default new admin forms to 1400 CNY total, show computed per-seat price, preserve legacy draft editing and display confirmed/total occupancy.
- [x] Verify real PostgreSQL create/update/publish/enrollment/snapshots, focused frontend tests, typecheck/lint/build, and desktop/mobile browser behavior. Record actual commands/results in 4A results before proceeding.

Files: `backend/internal/carpool/main_types.go`, `main_service.go`, `main_queries.go`, new migration 011 and pricing tests; `frontend/src/api/carpool.ts`, `utils/carpool.ts`, `components/carpool/CarpoolCarForm.vue`, `views/user/CarpoolLobbyView.vue`, locale entries and relevant tests.

Expected examples: 140000 fen / 2 = 70000; / 3 rounds to 46667; / 4 = 35000. A submitted seat price cannot override a supplied total. A legacy 9900-fen seat stays 9900 after migration and unrelated edits. New defaults must not rewrite receipts, refund amounts or historical snapshots.

Use focused `pnpm exec vitest run` and `pnpm run typecheck` from frontend. Backend tests use the cached Go 1.27.0 Docker runtime documented in `tools/carpool/PHASE_2A.md`, with disposable PostgreSQL for integration coverage. Run existing manual browser acceptance with updated occupancy and price semantics after rebuilding.

## 4B: Native Subscription Initialization

- [x] Inspect and plan native group bootstrap, account binding, member provisioning and native subscription assignment using existing services.
- [x] Add idempotent one-click preparation/activation, recovery and simplified connection UI.
- [x] Verify native protection for unprepared, expired, withdrawn and cross-car members before replacing the extra short lease dependency.
- [x] Verify backend, frontend and A/B native HTTP behavior; write 4B results before 4C.

## 4C: Native Data And Quota Automation

- [x] Add native subscription progress and account-window reads to My Car without reimplementing usage accounting.
- [x] Initialize limits at `1800 / N`; update the same group's weekly limit hourly to `(T - 100) / N` for valid estimates, preserving usage.
- [x] Align weekly windows with the upstream account, provide automatic and one-click reset with per-cycle deduplication and no request pause.
- [x] Verify native values, 450 to 475 adjustment, declining limits, reset races/retries/restarts, unavailable/stale snapshots and privacy; write 4C results. Three review findings were fixed and the expanded regression/follow-up review passed before 4D.

## 4D: End-To-End Delivery

- [x] Run affected phases 1-3 regression plus enrollment, initialization, activation, key creation, direct model request, native records, limit update, reset, expiry and revocation flows.
- [x] Verify A downtime does not add a short-lease rejection to otherwise valid B-native access; preserve cross-car and administrator credential isolation. The outage fixture closes A's database pool, not the PostgreSQL server or A process.
- [x] Supply fixed-version deployment/initialization instructions and local desktop/mobile preview after verified builds.
- [x] Audit all requirements against real evidence. Document separately authorized production TLS/egress/real-account checks rather than falsely claiming local fixtures prove production behavior.

4D's local audit and preview delivery complete the phase-4 development goal. This is not production acceptance; the operational exclusions and existing broader service-race limitation remain recorded in `PHASE_4D_RESULTS.md`.
