# Phase 2B Manual Enrollment UI Implementation Plan

> **For agentic workers:** Use executing-plans inline and test-driven-development. Do not dispatch subagents or commit existing phase 0/1/2A work. The user has approved the design and requested implementation.

**Goal:** Complete administrator enrollment/refund workflows and manual-mode user pages, then verify desktop/mobile and A/B access isolation.

**Architecture:** Preserve the approved catalog and vehicle CRUD. Add dedicated enrollment and manual-order components using the 2A admin APIs; share user-safe payment presentation. Keep pending commands immutable in memory and actor-scoped sessionStorage while their outcome is unknown. Clear private recovery data on logout or terminal session expiry. Reuse existing user search, dialogs, icons, pagination and server clock.

**Tech Stack:** Existing Vue 3/TypeScript, Vue I18n, Vitest, Playwright and Go/PostgreSQL integration harness; no new application dependencies.

## Boundaries

- Continue in the current checkout because prerequisite work is uncommitted. No reset, stash, branch transfer, production settings, payment calls, SMTP or remote pushes.
- Approved behavior is in `PHASE_2_MANUAL_DESIGN.md`; this plan does not reopen design approval.
- Offline confirmation is not channel verification. `pending` refunds must remain distinct from completed refunds. Full cars are not activated.

## Tasks

### 1. API, Types and State Helpers

Files: `frontend/src/api/carpool.ts`, `frontend/src/types/index.ts`, `frontend/src/utils/carpool.ts`, API/helper tests.

- [x] Run existing carpool tests as baseline.
- [x] Add failing API tests for `enrollManual`, `manualOrders`, `manualOrder`, `withdrawManual`, `refundManual`; each mutation forwards `Idempotency-Key` unchanged.
- [x] Define manual receipt/refund DTOs, `enrollment_mode`, `needs_review` and nullable `expires_at`, plus the read-only public manual flag.
- [x] Test and implement null-safe countdowns and manual admission predicates. Test records retain test-only reservation behavior.

Example assertion: `expect(client.post).toHaveBeenCalledWith('/admin/carpool/cars/car/members', input, { headers: { 'Idempotency-Key': 'retry' } })`.

### 2. Administrator Enrollment

Files: new `CarpoolEnrollment.vue`, `useCarpoolManualCommand.ts` and tests; existing `CarpoolView.vue`/`CarpoolCarForm.vue`.

- [x] Write tests for search response races, clearing selection on query changes, acknowledgement, exact amounts, future dates, immutable retry and no duplicate submission.
- [x] Search existing active users via `usersAPI.list(1, 20, { search, status: 'active' })`; show email and stable ID; never create users.
- [x] Render read-only price, channel choice, receipt time/reference/note and explicit receipt acknowledgement. Review vehicle/email/ID/amount before sending.
- [x] Freeze target/body/key after an uncertain network/5xx result. Provide same-request retry and manual-order reconciliation; do not silently rotate keys or allow a different enrollment while unresolved.
- [x] Add mode-aware vehicle actions, fixed manual refund policy, missing-contact warning and needs_review filter; maintain existing test-mode UI.

### 3. Administrator Orders and Refunds

Files: new `CarpoolManualOrders.vue`, shared `CarpoolPayment.vue`, component tests, `CarpoolView.vue`.

- [x] Add failing tests for cancelled-car pending refund visibility, required reason/confirmation, full refund bounds and disabled-additions historical operations.
- [x] Provide a paginated admin order view with car/status/refund filtering and identity, seat, receipt/recording details.
- [x] Use the original full amount; validate refund time and explicit acknowledgement; lock pending requests and preserve keys on unknown outcomes.
- [x] Refresh vehicle/member/order state after mutations. needs_review remains non-enrollable; no automatic refill or node activation.

### 4. User Pages and Localization

Files: `CarpoolLobbyView.vue`, `CarpoolMineView.vue`, `CarpoolStatus.vue`, `CarpoolPayment.vue`, bilingual carpool locale files and view tests.

- [x] Add failing tests proving manual records never show reservation/payment/cancel/countdown controls, even when the global manual flag is later disabled.
- [x] Show contact_info as escaped plain text only for currently recruiting manual cars with configured contact details; no QR codes or external checkout.
- [x] Present needs_review correctly, distinguish manual/test confirmation and pending/completed refunds, preserve historical orders and null service dates.
- [x] Preserve loading/errors/retry/background refresh and existing 3/2/1 column responsive catalog.

### 5. Integrated Verification and Preview

Files: browser Go harness, new `tools/carpool/manual-browser.mjs`, `tools/carpool/PHASE_2B.md`, `docs/carpool/PHASE_2B_RESULTS.md`, HANDOFF/design progress.

- [x] Extend the isolated browser harness with manual/read-only-manual origins and authenticated user-search fixtures, leaving production compliance unchanged.
- [x] Exercise create/publish/add/full/withdraw/refund, lost committed responses, search-response races (Vitest), long text, Chinese/English and desktop/mobile screenshots with overflow assertions.
- [x] Run frontend suite, typecheck/build, lint, affected Go tests and A/B lab. Explicitly record any missing checks.
- [x] Inspect screenshots; start a loopback-only synthetic preview and provide its URL without exposing credentials. Keep automatic test processes cleaned up.
- [x] Update documents with measured results; keep existing changes uncommitted.

Measured evidence, review fixes, artifact locations and exclusions are recorded in `PHASE_2B_RESULTS.md`.

## Commands

From `frontend/`: `pnpm test:run`, `pnpm lint:check`, `pnpm build`. Focused red/green runs use `pnpm exec vitest run <test paths>`.

Go 1.27.0 is available via the official Docker image from 2A. Integration uses isolated PostgreSQL; browser execution uses local Node/Chrome. The existing A/B lab is documented in `tools/carpool/README.md`, and requires a separately restored immutable baseline binary, never a baseline rebuilt from modified source.
