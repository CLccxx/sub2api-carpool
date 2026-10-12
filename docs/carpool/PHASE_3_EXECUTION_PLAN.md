# Phase 3 Control Plane Implementation Plan

> Execution: use writing-plans and executing-plans inline with test-driven-development. Preserve the existing working tree; no commits, branch changes, production credentials or deployment. Check each box only after fresh evidence.

**Goal:** Complete 3A, 3B and 3C from PHASE_3_CONTROL_PLANE_DESIGN.md: A-only member console, B-managed identity, keys, scoped usage, durable synchronization and verified isolation.

**Architecture:** Add an opt-in control plane alongside the existing main and OIDC node paths. The main database is authoritative for membership; node grants expire locally. Reuse native key and usage services behind fixed, authenticated commands, never a generic proxy.

**Tech Stack:** Go, PostgreSQL, Gin, existing Wire injection, Vue 3, TypeScript, Vitest and Playwright. Use the cached Go 1.27.0 Docker image because host Go is absent.

**Final status (2026-09-19):** 3A/3B/3C local scope completed. Checked items are backed by `PHASE_3A_RESULTS.md`, `PHASE_3B_RESULTS.md`, `PHASE_3C_RESULTS.md` and the numbered design audit in `PHASE_3_RESULTS.md`. This does not mean production rollout or a race-clean entire upstream backend; the broader service race gap remains explicit.

## Execution Rules

- Each task uses a failing test, observed RED, minimal implementation, observed GREEN and targeted regression before progressing.
- Run backend commands from `backend/` using the Docker wrapper documented in `tools/carpool/PHASE_2A.md`; mount source read-only for tests, existing `/tmp/carpool-go-mod` and `/tmp/carpool-go-build` caches, and Docker socket only for integration tests.
- Source edits use apply_patch. Formatting is mechanical gofmt. Do not modify applied migrations 001/002/003.
- Test fixtures may create active grants only within isolated tests. Production phase 3 does not activate cars, issue subscriptions, or convert receipts into balances.
- Retain full scope when a task spans turns. Record exact pending steps and fresh verification, not a partial-completion claim.

## 3A.1 Credential Envelope and Outbound Boundary

Files: create `backend/internal/carpool/control_crypto.go`, `control_crypto_test.go`, `control_http.go`, `control_http_test.go`.

- [x] Add credential tests for missing/invalid root, randomized round-trip, tamper, wrong root, main/node/generation binding and malformed envelope. Test contract:

```go
cipher, err := NewCredentialCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
require.NoError(t, err)
binding := CredentialBinding{MainID: "main", NodeID: "node", Generation: 1}
sealed, err := cipher.Seal(binding, "synthetic-admin-key")
require.NoError(t, err)
binding.Generation++
_, err = cipher.Open(binding, sealed)
require.ErrorIs(t, err, ErrControlCredential)
```

- [x] Run `go test -mod=readonly ./internal/carpool -run 'TestCredential' -count=1`; observe missing capability, implement AES-GCM with authenticated binding and a versioned bounded envelope, rerun with `-race`.
- [x] Test canonical HTTPS origins, exact configured allowlist, forbidden path/query/userinfo/IP, all DNS answers, private IPv4/IPv6 and actual dial peer mismatch; transport must disable proxy, redirects and insecure TLS.
- [x] Add HTTP fixtures for timeout, response limit, malformed envelopes, wrong identity/protocol, 401/403/423 and redirects. Verify error text contains neither secret nor remote body.
- [x] Implement fixed-path `ControlClient.Info(ctx, origin, credential)` using an internal injectable resolver/dialer for tests, bounded JSON decoding and opaque error classes. Run `go test -race -mod=readonly ./internal/carpool -run 'TestControl|TestCredential' -count=1`.

## 3A.2 Durable Connection State

Files: add `backend/internal/carpool/migrations/004_control_connections.sql`, `control_store.go`, `control_store_integration_test.go`.

- [x] Test migration twice after existing node grants and manual orders; derive migration count from embedded migration list rather than hard-coded 3.
- [x] Persist database-generated main/node instance identity, immutable node/car ownership, credential generation, encrypted value and connection verification result. Read DTO never contains ciphertext or key.
- [x] Test compare-and-swap create/update using expected generation: two saves of generation 0 produce one success and one conflict. Increment generation for replacement/disable and clear previous ready status.
- [x] Test late check response cannot mark a replaced/disabled connection ready, and failed validation leaves it unavailable. Test target hostname and instance pinning cannot be reassigned.
- [x] Implement transactionally using existing SQL/service errors and car-first lock order; hold no DB lock across HTTP. Run `go test -race -mod=readonly -tags=integration ./internal/carpool -run 'TestControlStore|TestMainMigration' -count=1`.

## 3A.3 Configuration, Wiring and Connection API

Files: `backend/internal/carpool/gate.go`, `control_config.go`, `backend/internal/config/config.go`, `carpool_config_test.go`, `backend/internal/service/carpool_control_provider.go`, `backend/internal/handler/carpool_control_handler.go`, `backend/internal/server/routes/carpool.go`, existing Wire files, middleware audit tests.

- [x] Extend config tests: defaults off; managed mode needs main/node/car identity, standard mode and a fixed permitted group; old OIDC constraints stay in old mode; unknown/mixed modes fail. Main control requires main mode, persistent encryption root and a nonempty origin allowlist.
- [x] Add authenticated main GET/PUT connection and POST check endpoints; all credential mutations honor configured step-up and omit whole bodies from audit. Strict JSON rejects arbitrary remote paths and fields.
- [x] Add B `GET /api/v1/admin/carpool-control/v1/info`, returning only protocol/capabilities and pinned identifiers. Store node instance UUID in its DB so replacing DB is detected.
- [x] Test routes missing when disabled, main/node mismatch, 401/403, compliance 423 and step-up rejection. Verify provider fails closed on invalid master key and runs migrations only when enabled.
- [x] Run affected unit/race integration tests and server build. Write `PHASE_3A_RESULTS.md` only from actual output.

## 3B.1 Managed Members and Local Gate

Files: additive `005_managed_members.sql`, `managed_members.go`, `managed_grants.go` (including the managed gate), accompanying tests in `backend/internal/carpool`; `backend/internal/service/node_access.go` and affected authentication entry points.

- [x] Persist stable A subject IDs, B mappings unique by main/subject and B user ID, no email matching. Provision B user and mapping in one transaction with role user, zero balance, restricted public groups and non-login credentials; no default-subscription hooks.
- [x] Test concurrent provisioning/replay, rollback after user insert, stable IDs across restart and different nodes, no OIDC rows or grants created.
- [x] Test managed member password/OAuth/reset/session rejection while separate admin recovery works; legacy OIDC tests remain unchanged.
- [x] Implement managed CheckUser as a fresh DB membership/user/time check independent of API-key cache. Test warmed keys immediately fail after pause/revoke/delete and expire on lease/service end.
- [x] Run `go test -race -tags=integration -mod=readonly ./internal/carpool ./internal/server/routes` and relevant authentication unit suites.

## 3B.2 Delegated Keys and Scoped Usage

Files: `backend/internal/service/carpool_managed_keys.go`, `backend/internal/carpool/control_queries.go`, `backend/internal/handler/carpool_control_handler.go`, related DTOs, tests and additive `006_managed_key_commands.sql`.

- [x] Add member provisioning, grant and key mutation routes under the pinned B protocol. Key create requires stable operation ID, current active eligibility, fixed group and capped service expiry; PATCH permits only name/status and DELETE rechecks owner.
- [x] Reuse APIKeyService via a transaction-aware path for atomic command/key persistence. Test committed-response-loss yields one key, mismatched replay conflicts, deleted key is never recreated, revoked caller cannot recover full secret.
- [x] A operation journal stores actor/car/subject/hash/downstream ID, never secret response. Do not use response-storing generic idempotency middleware.
- [x] Add A member endpoints scoped to car and authenticated actor. Resolve B ID internally; existing B admin key lists/usage use fixed user filters. Limit dates, page size and response DTO to approved fields.
- [x] Test two users on B and a second car on C; attempts with arbitrary key IDs, node IDs, URLs, user filters, group, balance and role are rejected. B failure is unavailable, never empty successful data.

## 3B.3 Versioned Grants and Durable Reconciliation

Files: `backend/internal/carpool/control_jobs.go`, `control_sync_publish.go`, `managed_grants.go`, additive `010_control_sync.sql`, `backend/internal/service/carpool_control_worker.go`, provider/shutdown wiring and integration tests.

- [x] Atomically persist desired version/hash and pending command before sending. B validates main/node/car/subject, DB clock, status, monotonic version, same-version hash and <=15-minute lease bounded by service end.
- [x] Test 2/5/15/30/60-second retry schedule with jitter and a six-attempt automatic budget, expired claim recovery, two workers, restart, response loss, stale active after revoke and credential generation changes.
- [x] Reconcile every 60 seconds against authoritative memberships and live users. Prepared members never become active through sync. Deleted/disabled users stop renewal and get revoked; authentication/compliance/protocol failures become operator-visible terminal errors.
- [x] Test A outage permits model requests only within existing lease, B outage does not spill to C, stale API-key caches cannot bypass node gate, active streams may finish.
- [x] Record fresh 3B race/database/HTTP results and remaining production limitations in `PHASE_3B_RESULTS.md`.

## 3C.1 Admin Connection and Member Console

Files: `frontend/src/api/carpool.ts`, `frontend/src/components/carpool/CarpoolNodeConnection.vue`, `CarpoolNodeConsole.vue`, corresponding tests, `frontend/src/views/admin/CarpoolView.vue`, `frontend/src/views/user/CarpoolMineView.vue`, new locale module and auth cleanup integration.

- [x] Write API/component tests for fixed paths/types, connection masked reads, blank credential replacement, save/check races, terminal failures and explicit retry.
- [x] Add compact administrator connection controls within current vehicle workflow, keeping lobby unchanged. Credential input uses password type and clears on close/save/logout, never persisted.
- [x] Add per-car keys and usage tabs; loading, empty, unavailable, unactivated and denied states remain distinct. Existing A-native keys/usage pages remain separate.
- [x] Add key create/rename/enable/disable/delete controls with pending operation metadata recovery and ephemeral full-key display only on legitimate create/replay. Logout/actor/car switches clear secrets and discard late responses.
- [x] Test desktop/mobile long names, Chinese/English parity and unactivated disabled controls; run `pnpm test:run`, `pnpm lint:check`, `pnpm build` in frontend.

## 3C.2 End-to-End and Completion Audit

Files: `tools/carpool/control-browser.mjs`, isolated native browser/model/HTTP Go integration fixtures, `docs/carpool/PHASE_3C_RESULTS.md`, `PHASE_3_RESULTS.md`, `HANDOFF.md` and roadmap. The native fixtures replace the originally proposed standalone managed-lab scripts rather than duplicating them.

- [x] Test A email-account login without B member session, admin enrolment/prepared mapping, test-only active fixture, keys and actual model-stub usage on B/C, revoke/lease expiry, node identity replacement, connection failure and restart.
- [x] Playwright desktop/mobile screenshots and DOM checks must show actual data without overflow or overlap. Block outside requests; use only synthetic credentials and no real email, purchases or production resources.
- [x] Run previous 12 manual, 18 legacy and 16 OIDC A/B browser groups with current binaries, plus production authentication/compliance route suites.
- [x] Run affected backend race tests, full backend lint, embedded server build and frontend full test/type/build checks. All needed command sessions must reach terminal status before reporting results.
- [x] Audit every numbered section of PHASE_3_CONTROL_PLANE_DESIGN.md against actual code and fresh output; explicitly leave missing evidence incomplete. Only then mark 3A/3B/3C and the full goal complete.

## Live Progress

- [x] Written design approved by user; current branch and prerequisite work preserved.
- [x] Confirmed existing Go/PostgreSQL Docker images are available; host has no Go.
- [x] 3A.1 credential and network boundary implementation, observed RED/GREEN and race coverage.
- [x] 3A.2 durable connection state, identity pinning, generation/check-operation races and historical migration regression.
- [x] 3A.3 configuration, HTTP/auth/compliance/step-up/audit, Wire, full affected unit/database regression, build and full backend lint (0 issues); see PHASE_3A_RESULTS.md.
- [x] 3B.1 backend identity/provision/grant contracts: stable A subjects, atomic B internal users, managed version/lease gate, no member web sessions and automatic durable preparation are implemented and tested.
- [x] 3B.2 backend key/usage contracts: B native transactions, A journal/mapping/barriers and the six authorized endpoints are tested, including real A-to-B/C TLS calls against full native schemas. That test inserts usage records; the additional 3C production-gateway test separately verifies actual model-generated usage.
- [x] 3B.3 backend verification: migration 010, transactional publication, bounded claim/retry/reconciliation worker, status/retry APIs and shutdown wiring are implemented. The historical database race checkpoint passed (78.730s / 15.422s); latest results are in PHASE_3B_RESULTS.md. Model/browser acceptance has since passed. The known broader service race gap remains separate in PHASE_3B_PROGRESS.md.
- [x] 3C foundation: config-only public/SSR control flag, opt-in frontend flag, expanded service types and fixed-route API adapters. Full frontend 290-file/2266-test regression, lint and build passed.
- [x] 3C Vue controls and sensitive-data lifecycle: 13 connection and 17 console tests; final full frontend 293 files / 2311 tests, lint/type/build passed.
- [x] Final A/B/C model/browser/HTTP acceptance passed with rebuilt test runner, including actual gateway-generated usage, in-flight stream completion and denied post-revocation requests. Historical 12/18/16 groups passed.
- [x] Final integration-tag lint reports 0 issues. The prepared-only preview was verified in the browser and retained for eight hours; no verification command remains running.
- [x] Full-phase design audit and documentation completed. Stage 4 activation, production resources and broader unrelated service races remain outside the completed local scope.
- [x] Final `git diff --check` passed; branch remains `codex/phase0-node-access`. No commit, reset, push or worktree cleanup was performed. Preview PID 39109 was checked against its test-runner command and the admin/member pages were verified in the browser.

### 3B Implementation Findings

- `userRepository.Create` creates an email identity in addition to the user; managed provisioning uses a bounded SQL transaction with fixed fields instead, avoiding that hook and default subscriptions/credits. Verify on the full upstream schema as well as the focused DB fixture.
- `APIKeyRepository.Create` and `GetByID` were made transaction-aware after an observed regression test. Update already used the context transaction. Current B commands reuse native Create/Update/Delete, persist only command metadata and invalidate key auth caches after commit.
- Native read endpoints require `bound-admin-reads` in the fresh info response. B checks delegation target headers in the authenticated admin group as well as the protocol-specific mutation handlers. A cannot trust a stale ready flag or silently query an older build that ignores the headers.
- Ent's client/transaction already expose ExecContext and QueryContext, so a managed key transaction can lock the grant and persist the command in the same ent transaction as native key creation. Do not create an independent SQL transaction around it.
- Managed mode must deny AuthService member session creation regardless of a successful model grant. APIKeyService remains connected to the local managed gate.
- New B protocol routes must bind main/node/car/instance from fixed headers and validate subject UUIDs, never accept user ID/group/role/balance or a caller-chosen path. A must still persist tasks before dispatch and never publish active grants for waiting-deployment members.
