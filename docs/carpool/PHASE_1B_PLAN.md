# Phase 1B Frontend and Local Integration Plan

> Execute with test-driven-development and verification-before-completion. Use subagent-driven-development for independent, bounded work. Preserve all phase-0/1A edits in the existing checkout. No commits, pushes, real payments, compliance acceptance, SMTP or production deployment.

**Goal:** Implement the approved PHASE_1_DESIGN section 8 and verify P1-16 on desktop/mobile using local test orders.

**Architecture:** Existing Vue/Pinia/router/i18n, AppLayout, DataTable, BaseDialog and pagination. The backend remains authoritative for ownership, capacity and time. No confirmation endpoint or payment UI. Native responsive lists on mobile; desktop tables for administration. Reuse existing icons. No new dependencies.

**Status (2026-09-16):** Tasks 1-5 complete for local acceptance. See `PHASE_1B_RESULTS.md` for evidence, review fixes, timing and limitations. The browser harness needs PostgreSQL only; Redis is exercised separately by the A/B lab. Interactive preview uses a compiled frontend with its test backend, not a frontend-only server. No commits or remote push.

## 1. Config Visibility (Backend, Independent)

- [x] Add a failing test for public settings and SSR injection exposing `carpool_main_enabled` and `carpool_test_orders_enabled`; both false by default and on B. Main-on/tests-off is read/admin-only. Fields derive from config, never mutable DB settings.
- [x] Implement fields in service public settings, injection payload, response DTO and handler. Verify `go test -tags=unit ./internal/service ./internal/handler ./internal/handler/dto` with the temporary Go toolchain.

## 2. Frontend Contract and Navigation (Local)

- [x] Add `frontend/src/api/carpool.ts` with `CarInput`, `Car`, `Order`, `Membership`, `Page<T>` matching Go DTOs. Expose `listCars(params, admin=false)`, `getCar(id, admin=false)`, `reserve(id,key)`, `mine(params)`, `cancelOrder(id)`, `createCar(input)`, `updateCar(id,input)`, `publishCar(id)`, `cancelCar(id,reason,confirmMembers)`, `members(id,params)` through named `carpoolAPI`. API tests verify paths/body and the required `Idempotency-Key`.
- [x] Add `carpool_main_enabled?: boolean`, `carpool_test_orders_enabled?: boolean` to PublicSettings, opt-in registry flag, `requiresCarpool` metadata and routes `/carpool`, `/carpool/mine`, `/admin/carpool`. Unknown/failed settings fail closed for these routes. Sidebar uses same flag.
- [x] Add shared carpool helpers/components for currency, timestamps, server-clock countdown, statuses and rule details. Browser clock skew must not extend a hold. Reuse a reservation key across uncertain network retries, scope storage to user/car, rotate after a known terminal order. No forged client state or user_id sent.
- [x] Add focused API/feature guard/helper tests first; run Vitest red then green.

## 3. Admin Vehicles (Bounded Page Task)

- [x] `frontend/src/views/admin/CarpoolView.vue` plus carpool form/detail components: list/filter/page, create/edit draft, freeze terms on publish with confirmation, cancel with reason and explicit confirmed-member acknowledgement, member list with pagination. Include loading/empty/errors/retry, request races and double-submit guards. Use server result to refresh after mutations/409. No node activation or payment confirmation.
- [x] Required form: name, 2/3/4 seats, CNY decimal input converted exactly to integer minor units, service days, local datetime converted to ISO, delivery hours, refund rules. Enforce byte lengths/numeric limits from API; do not invent real prices.
- [x] Tests cover create/edit/publish/cancel payloads and acknowledgements, stale request handling, member pagination and error display. i18n module `frontend/src/i18n/locales/carpool.ts` exports `carpoolZh`/`carpoolEn` (all three pages); root locale modules import it under `carpool`.

## 4. User Pages (Local)

- [x] `/carpool`: paginated published cars, confirmed/held/available counts, terms/detail and current participation; reserve only when flags and server state permit, guarded retries, no private member data. My-state loading failure disables reservations until resolved.
- [x] `/carpool/mine`: paginated/filterable order snapshots, pending countdown and cancel, waiting/cancelled membership, no active service dates/entry. All transactions marked test. Refresh after actions and while visible to observe CLI confirmations; stop timers on unmount.
- [x] Vitest tests cover reservation retry key reuse, current-user state, disabled/expired/full/cutoff states, conflict refresh, cancellation, snapshots, error/empty/loading and no real-payment/activation controls.

## 5. Local Browser Validation and Handoff

- [x] Build frontend and embedded server; run all frontend tests, lint and typecheck plus affected backend tests. Review spec then code quality, fix findings, rerun affected checks.
- [x] Add reproducible local-only lab under `tools/carpool/` using isolated PostgreSQL/Redis and synthetic users. Exercise desktop/mobile pages via Playwright with real backend/JWT/CLI. Do not accept compliance terms: verify real guard first; positive admin workflow may use a clearly documented test-only harness, not weakened production code.
- [x] Check screenshots, horizontal overflow, dialog text/controls and loading/conflict/off/node behavior. Cover create/edit/publish, reserve/cancel/CLI-confirm, full waiting car, members and no activation. Re-run phase-0 A/B lab.
- [x] Record exact pass/fail/limitations in `docs/carpool/PHASE_1B_RESULTS.md`; update HANDOFF/IMPLEMENTATION_PLAN/design. Clean lab-owned resources. Start a local frontend dev server on an available port for user preview, clearly disclose any backend requirement. No real payment/deployment.
