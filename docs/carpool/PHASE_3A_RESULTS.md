# Phase 3A Verification Record

Date: 2026-09-19 (Asia/Shanghai).

Status: historical 3A checkpoint, recorded before 3B changes. These results do not claim verification of later changes. The completed 3B/3C implementation and final full-phase audit are recorded in `PHASE_3B_RESULTS.md`, `PHASE_3C_RESULTS.md` and `PHASE_3_RESULTS.md`.

## Implemented Scope

- Independent base64 32-byte AES-GCM root, versioned randomized envelope, main/node/generation authenticated binding, corruption and wrong-root rejection. Configuration rejects reuse of JWT/TOTP root material.
- Exact HTTPS origin allowlist maintained by deployment configuration; no paths, credentials, queries, fragments or literal IP targets. Both default proxy use and redirects disabled. All resolved A/AAAA addresses checked, numerical dial only, actual peer checked, TLS trust/hostname checks, timeouts and bounded responses. No production private-network bypass.
- Additive migration 004: database instance role/UUID and encrypted node connections. Existing migrations untouched. Connection host/instance pinned once verified; saving/disable increments generation. Same-generation checks also carry distinct operation IDs so late checks cannot overwrite newer results.
- Main administrator GET control identity, GET car connection, PUT node connection and POST check routes; existing administrator auth/compliance/audit groups, with configured step-up on credential save/check. No secret echo; private responses use no-store.
- Managed B protocol info endpoint and persistent database identity. Managed mode requires canonical main/node/car UUIDs and positive group, excludes OIDC and rejects legacy node grants. Existing OIDC remains default when node mode is selected without the new mode.
- No member provisioning, service grants, user keys, model proxying or phase 3 frontend in this slice. Info advertises only `connection-check`, not unimplemented capabilities.

## Fresh Evidence

Commands used existing `golang:1.27.0` and `golangci/golangci-lint:v2.13.0` Docker images, read-only source mounts for tests/build, dedicated temporary caches and disposable PostgreSQL 18.1 testcontainers. No host Go installation, application dependency changes or production services were used.

| Check | Observed result |
| --- | --- |
| Pre-change `go test ./internal/carpool` | PASS |
| New credential, HTTP, connection store, mode and handler tests before implementation | RED observed for missing code; audit omission failed its assertion |
| Root reuse test before guard | RED: both JWT and TOTP reuse incorrectly succeeded; guard then implemented |
| `go test -tags=unit` on carpool/config/handler/service/middleware/routes/test-order/membership CLI | PASS; membership CLI has no test files |
| Final `go test -p 2 -race -tags=integration` on carpool/config/routes/test-order CLI | PASS: carpool 32.503s, config 3.801s, routes 15.513s, CLI 2.881s |
| `go build` server and both carpool CLIs after Wire regeneration and final configuration guard | PASS; final rebuild exited 0 |
| Full backend lint | PASS: 0 issues after fixing three unchecked type assertions and one private-struct test serialization warning |
| Focused security/config/handler/routes/middleware race rerun after lint fixes | PASS in all five packages |

The database checks include historical OIDC grants and phase 1/2 transactions, repeated migrations, two competing saves, changed encryption root, credential replacement during a check, overlapping checks at one generation, disabling during a check, identity mismatch and zero service grants/dates. TLS fixtures verify wrong root/hostname, 401/403/423, redirects, large/malformed payloads and deadline expiry. Mixed public/private DNS answers are rejected before dialing.

HTTP tests use actual administrator authentication for synthetic B keys (A key rejected by B), ordinary JWT forbidden, persistent node-info response, and main connection save/replay. Positive harnesses deliberately omit compliance without accepting a declaration; full production registration separately proves compliance 423 and step-up middleware attachment. These are not yet the real A/B/C member browser acceptance required by 3C.

One test-fixture defect was corrected: a compliance test's fake auth omitted AuthSubject, causing 401 at the handler rather than 423. The fake now matches the existing authenticated test pattern; no production auth bypass was introduced.

## Work Remaining at This Historical Checkpoint

- 3B: stable main subjects, internal node members, no member login/session, native key transactions, scoped queries, versioned leased grants and durable reconciliation.
- 3C: administrator connection UI, member key/usage UI, secret lifecycle, desktop/mobile and A/B/C plus old-browser regressions.
- No production TLS/DNS deployment, SMTP, payments, resources, releases or real credentials. Administrator key still has full B authority; encryption and A endpoint allowlists do not downscope it.
