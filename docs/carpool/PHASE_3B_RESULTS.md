# Phase 3B Local Verification

Date: 2026-09-19 (Asia/Shanghai).

Status: backend implementation and local acceptance completed, including the
model/browser checks delivered with 3C. `PHASE_3B_PROGRESS.md` retains the earlier
RED/GREEN checkpoints and known unrelated service race failures; it is not the
current remaining-work list. Overall scope and release limits are in
`PHASE_3_RESULTS.md`.

## Delivered

- Additive migrations 005-010 persist managed members, native key commands,
  main service windows, remote mappings, create-response barriers and durable
  synchronization. Existing migrations 001-003 are unchanged.
- A assigns stable subjects independently of numeric user IDs. B atomically
  provisions a fixed-role internal user and unique mapping, with zero balance,
  restricted groups, no email identity/default subscription and no usable login
  password. Replays and concurrent requests return the same mapping.
- Managed mode is separate from legacy OIDC. Member web sessions are denied;
  independent administrator recovery remains. Model authorization checks fresh
  local database state, service dates and a maximum 15-minute lease on every
  request, including requests with a warmed API-key cache.
- B's fixed, identity-bound commands reuse native APIKeyService validation and
  ent transactions for create, rename, enable, disable and delete. Command
  metadata and the resource commit atomically. Post-commit cache invalidation
  survives request cancellation. Deleted keys retain a tombstone and cannot be
  recreated by replay; revoked callers cannot recover the full secret.
- A resolves the selected car, subject, B user and permitted group from the
  authenticated membership. It never accepts an arbitrary remote URL or caller
  supplied remote user/group. Native B key/usage reads enforce the bound target,
  while A validates owners and returns whitelisted fields. Dates are UTC, at most
  31 inclusive days; pages contain at most 100 items.
- A's create journal contains operation/actor/car/subject/hash/downstream ID, not
  a plaintext key or response body. Fixed operation IDs recover lost committed
  responses. Credential replacement, revoke, deletion and disable barriers
  prevent stale responses from exposing a secret.
- Desired grant/version and pending work are persisted before sending. The
  worker uses recoverable claims, rechecks authority before dispatch, rejects
  stale acknowledgements and reconciles every 60 seconds. Retries use jittered
  2/5/15/30/60-second delays, then require operator retry after six attempts.
  Authentication, compliance, protocol and identity failures stop automatic
  retry. Failed nodes do not silently redirect work to another car.
- Console status, paginated synchronization status and administrator retry
  endpoints are wired through existing authentication, compliance, audit,
  step-up and usage-rate controls. Sensitive responses use no-store.
- Ordinary enrollment and reconciliation produce prepared mappings only.
  There is no production activation command or implicit credit/subscription.
  Existing pre-service refund/cancellation commands reject served records.

## Evidence

Exact checkpoint commands and RED/GREEN observations remain in
`PHASE_3B_PROGRESS.md`; final end-to-end results are in `PHASE_3C_RESULTS.md`.

| Boundary | Verification |
| --- | --- |
| Stable identities, atomic provisioning, grant versions and lease windows | `managed_members_integration_test.go`, `control_subjects_integration_test.go` |
| Native key transaction rollback, concurrent replay, expiry-at-enable, HTTP ownership | `api_key_repo_carpool_integration_test.go`, `carpool_keys_http_integration_test.go`; race passed, 4.789s |
| A journal persistence, response loss, revoked/rotated/deleted response barriers | `control_member_store_integration_test.go`, `control_members_http_test.go`, `control_keys_http_test.go` |
| Scoped reads and strict HTTP inputs | `control_queries_http_test.go`, member/target handler tests, production route suites |
| Queue claims, restart, generation changes, unsent acknowledgements and finite retries | `control_jobs_integration_test.go`, `control_jobs_test.go`, worker tests |
| No implicit activation and pre-service refund limits | `control_status_integration_test.go`, `main_service_windows_integration_test.go` |
| Native A/B/C TLS, two users, node outage and cross-car separation | `TestControlNativeHTTPMainToTwoNodes` |
| Production gateway writes real usage; revoke, pause, restart, service/lease expiry | `TestControlProductionModelNodes`; includes in-flight stream completion after revoke |
| A-only login, real Vue flows, lost response/reload and mobile isolation | `TestControlBrowserNative`; 14 browser groups |

The final affected PostgreSQL/race run passed: carpool 75.824s, routes 14.629s,
config 3.459s and test-order CLI 2.396s. Targeted unit/race passed for service
1.483s, handler 2.738s and middleware 1.050s. Complete default-tag backend lint
reported 0 issues and the embedded server/both CLIs built successfully.

## Limits

Full ordinary service unit tests passed (176.950s), but the broader service race
suite did not. Existing monitoring/Grok/usage-cleanup/OpenAI test shared-state
races are documented and independently reproduced in `PHASE_3B_PROGRESS.md`.
These results do not claim a race-clean whole backend or a tested clean upstream
checkout.

An A outage prevents management and lease renewal, not necessarily an already
authorized B request until its existing lease expires. Tests stop renewal and
expire the local lease; they do not simulate a production network partition for
15 wall-clock minutes. Active streams already admitted may finish; new requests
after revoke are denied. Internal test SQL supplies active/billing fixtures only.

Node administrator keys retain full node authority. Production access controls,
key backup/rotation, public TLS/egress, deployment, real model accounts and service
activation still require phases 4/5. No real credentials, SMTP, payments,
purchases, remote commits or deployment were used.
