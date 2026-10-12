# Phase 0 Authentication Implementation Plan

> Execution: use executing-plans and test-driven-development, inline in the current task. User approved the scope on 2026-09-16. Preserve unrelated baseline failures and do not push or deploy production services.

**Goal:** Reproduce the original A/B authorization gap, then prove local node membership enforcement with a real local OIDC provider and a fake model upstream.

**Architecture:** A remains the default application. B enables an opt-in node gate backed by its own PostgreSQL database, keyed by node/car/issuer/subject. Gate reads do not cache successful authorization. A local CLI applies monotonic grants; it is a test/operator fixture, not the future network synchronization API.

**Tech Stack:** Existing Go/Gin/PostgreSQL/Redis/Vue; local Keycloak 26.7.3; Node test orchestration and browser automation. Independent hosts, databases, cache processes and signing keys. All published ports bind loopback.

**Execution status (2026-09-16):** Implemented and locally verified. See `PHASE_0_AUTH_RESULTS.md` and `../../tools/carpool/README.md` (repository path: `tools/carpool/README.md`). The policy-only initial red-run transcript was not retained; do not infer that evidence from the final green suite. OIDC red cases, the real-PostgreSQL identity-binding failure and the pending-registration ordering failure were observed before their corresponding fixes.

**Follow-up:** The two original frontend fixture failures were subsequently fixed; the full 2163-test frontend suite passes. See `PHASE_0_FRONTEND_FIXES.md`. Original baseline failure evidence remains unchanged.

## Tasks

### 1. Member Policy and Persistence

Files: `backend/internal/carpool/{gate.go,gate_test.go,migrate.go,migrations/001_node_memberships.sql}`, `backend/cmd/carpool-membership/main.go`.

- [x] Add table-driven policy tests for wrong-node, wrong-car, revoked, paused, not-started, expired and expired-lease grants; query tests cover absent grants and fail-closed database errors. SQL behavior is additionally covered by real PostgreSQL/E2E, not only the policy helper.
- [ ] Retain the initial policy-only failing-run transcript (not retained; final execution passed).
- [x] Implement a separate checksum-tracked migration stream; do not edit upstream migrations. Add atomic upsert requiring a strictly newer version, exact duplicate idempotency, and conflicting/stale version rejection.
- [x] Add a stdin-JSON CLI using an environment DSN; reject unknown fields, wrong target, role injection, invalid intervals and leases longer than 15 minutes. No HTTP management endpoint.
- [x] Run policy and real PostgreSQL integration tests, including concurrent/stale grant application.

### 2. Authentication Enforcement

Files: `backend/internal/config/config.go`, `backend/internal/carpool/gate.go`, service auth/API-key files and Wire providers; middleware JWT/API-key/Google files; focused tests.

- [x] Add tests showing that node mode rejects unentitled sessions and Key creation, including a warmed API-key cache E2E.
- [x] Reproduce the original authorization gap in the lab and the incorrect verified_at assumption with PostgreSQL before correcting it. Add a red/green regression for pending registration before identity binding.
- [x] Add disabled-by-default `carpool_node` configuration. Require node/car/issuer and strict OIDC settings when enabled. Missing runtime gate is an error, never an implicit allow.
- [x] Enforce membership in session issuance/refresh, authenticated user APIs, Key creation and both API-key authentication paths, before simple-mode or billing exemptions. Dedicated admin management login remains possible; admin model keys do not bypass the node gate.
- [x] Suppress signup, bind and promo grants on B; authorize the verified OIDC identity before creating a local account. Pending email signup commits binding before gated token issuance and rechecks revocation.
- [x] Generate Wire output and run focused tests and lint. Preserve A behavior when the gate is disabled.

### 3. OIDC Security Checks

Files: `backend/internal/handler/auth_oidc_oauth{,_test}.go`.

- [x] Add negative tests for missing/expired exp, missing/future iat, wrong issuer/audience/nonce, and azp mismatch.
- [x] Run tests to demonstrate baseline failures, then require the missing claims and validate issued-at.
- [x] Replace the verified-email fast-path URL tokens with the existing browser-bound pending-login exchange. Test that callback Location contains no access/refresh token, pending flow remains usable and replay is refused.

### 4. Reproducible Local A/B Lab

Files: `tools/carpool/` orchestration, fake upstream and browser test; local config/realm data generated into an ignored private runtime directory.

- [x] Preserve the original embedded server binary before rebuilding.
- [x] Launch uniquely named test PostgreSQL/Redis containers plus Keycloak; use separate A/B hostnames and an IdP issuer reachable by the host processes/browser.
- [x] Generate independent random credentials without committing them; use no real upstream/payment/SMTP credentials. Model/pricing traffic goes to local fixtures and browser external requests are blocked.
- [ ] Fully disable backend external metadata/update traffic (not implemented; no claim of comprehensive egress isolation).
- [x] Run original binary first: prove A-to-B SSO and demonstrate that an unrelated identity can obtain B access/Key without a vehicle grant.
- [x] Run modified B: verify no-grant denial, manual grant, direct B-to-stub success, cross-site JWT/Key denial, admin/ownership denial, revoke/expire/pause/lease expiry denial, and monotonic version checks.
- [x] Warm the Key cache before revocation; compare fake-upstream request counts before and after denied requests. Distinguish HTTP rejection from upstream unavailability.
- [x] Capture sanitized machine-readable results and browser evidence. Shut down only lab-owned processes/containers, retaining private logs locally for investigation.

### 5. Regression and Handoff

- [x] Run `go test -tags=unit -count=1 ./...`, `CI=true go test -tags=integration -count=1 ./...`, lint, embedded build and `make test-frontend`.
- [x] Record actual pass/fail/skip states, including the two unrelated frontend full-suite failures. No claim of C-node isolation, real synchronization recovery, production TLS, payment, SMTP or fixed public egress.
- [x] Update baseline/handoff with exact commands and remaining limitations. Do not commit secrets or push changes.
