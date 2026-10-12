# Phase 4D Results

Date: 2026-09-23 (Asia/Shanghai).
Status: phase 4D local acceptance complete. Phases 4A-D have passed their sequential local gates; production operations remain outside this acceptance.

## Delivered Scope

- `native_model_integration_test.go` uses independent embedded production B/C processes, real PostgreSQL subscription charging and Redis cache invalidation, and a loopback model-response fixture. It covers four-member initialization, explicit activation, zero-balance billing, native records, 450 to 475 without clearing usage, lower-limit rejection before upstream traffic, weekly reset/replay, key disable/re-enable/delete, cross-car isolation, revocation and expiry.
- A unavailability is simulated by closing A's `sql.DB` pool and asserting it cannot be used. PostgreSQL itself is not stopped, and this is not an A process-kill or production network-partition test. A valid B subscription continues after the historical grant lease expires; the same B rejects native subscription expiry.
- The bound OAuth row has synthetic window metadata and is not scheduled for model traffic. A local API-key account transports fixture model responses. These tests do not prove real OAuth authorization, token refresh, account estimation accuracy or real upstream resets.
- Native billing assertions wait for both per-key record count and matching committed subscription charges. After reset they use the preceding usage-log ID as a high-water mark, so delayed old charges cannot masquerade as the new request's charge.
- Separate A/B Compose overlays and environment examples require an explicit custom image and persistent secrets. They configure manual enrollment, managed nodes, auto-group initialization and explicit proxy trust. No image was built/published or production service launched by these instructions.
- The test-only control preview now starts the existing production synchronization worker. Its browser regression never calls the test drain endpoint in background mode. The preview also exposes the real carpool create/edit/publish/cancel, manual member and receipt routes, with authenticated synthetic-user lookup. Its datasets include four- and two-member full cars plus a three-member recruiting car at a 1400 CNY total price.

## Requirement And Evidence Audit

All paths below are relative to this repository. These assertions complement the preceding phase results; they do not claim production acceptance.

| Requirement | Executed assertion or implementation evidence |
| --- | --- |
| Preserve stages 1-3; no second registration/password synchronization | `control_native_http_integration_test.go: TestControlNativeHTTPMainToTwoNodes` checks internal identities do not copy A email/password; `control-browser.mjs` logs in only at A. |
| Offline receipts, administrator enrollment, no self-reservation | `manual_integration_test.go`, `carpool_manual_integration_test.go: TestCarpoolManualHTTP`, `manual-browser.mjs`; the current control preview also exercises the real routes. |
| 1400 total divided by fixed 2/3/4 capacity, confirmed occupancy, historical receipts unchanged | `pricing_test.go`, `pricing_integration_test.go`, the total-price/occupancy assertions in `manual-browser.mjs`; see `PHASE_4A_RESULTS.md`. |
| Automatically initialize native group/account/member mappings, then explicitly activate one fixed service window | `native_subscription_integration_test.go: TestNativeSubscriptionBootstrapAndAccess`, `TestNativeCarPreparationActivationAndRetry`; native browser preparation/activation and production model lifecycle. |
| No usable key before activation; no accidental renewal on retry | The subscription initialization tests and the prepared-key rejection in `TestNativeSubscriptionProductionLifecycle`. |
| Initial 1800/N, then hourly (T-100)/N; only weekly limits | `native_quota_test.go`, `native_quota_integration_test.go`, `carpool_quota_worker_test.go`; production model assertion changes 450 to 475 and preserves charged usage/expiry. |
| Falling quota handled by B, without an A model proxy or request pause | Production B returns `429 USAGE_LIMIT_EXCEEDED` without another upstream call; independent C remains usable. |
| My Car shows B's own subscription, records, account percentage and reset window | `control_native_http_integration_test.go`, `native_quota_http_test.go`, `carpool_quota_source_test.go`, native browser quota/usage tabs; native model charges and records are retrieved through A. |
| Account-aligned weekly reset, idempotent automatic/manual synchronization | `native_quota_integration_test.go`, `native_quota_correction_integration_test.go`, `carpool_subscription_anchor_test.go`; production reset/replay preserves the new request's charge and old history. |
| Stale/missing data is not zero, corrections do not double-reset, cache failures remain retryable | Coherent weekly-observation source tests, quota correction tests, and `native_quota_cache_integration_test.go`; see the RED/GREEN evidence in `PHASE_4C_RESULTS.md`. |
| Only the member's car/data is exposed; admin credentials remain private | Control HTTP/member/key tests, carpool route permission tests, browser member/admin denial and B/C isolation assertions. |
| No short-lease dependency on A after native initialization | Production model lifecycle closes A's database pool and expires the legacy lease, then checks B success and subsequent native expiry rejection. |
| Repeatable deployment instructions, no assumption that arbitrary upstream B is compatible | `tools/carpool/PHASE_4.md`, `deploy/carpool/*`, and 14 `deployment-config.test.mjs` checks. |

## Verification Record

- Before this final pass, the strengthened native model lifecycle passed three consecutive runs: 9.01s, 8.31s and 8.33s. Historical production model regression passed in 12.04s. Independent read-only follow-up review closed the async-billing and proxy-trust findings; review itself did not run tests.
- Historical browser regression during 4D: manual flow 11.56s (12 groups), legacy order/lobby 13.35s (18 groups), OIDC lab 16 groups. The preserved original binary's build metadata does not include a VCS revision; this was not a fresh clean-upstream rebuild. OIDC artifacts: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-jpD5Ds`.
- Current frontend full suite: 293 files / 2327 tests, 15.79s. Lint and typecheck exited zero; locale checks and production build passed (Vite 14.92s). Existing Vue test-stub, Browserslist, mixed-import and bundle-size warnings remain.
- Current Compose configuration verification: 14 tests, all passed. It only parses configuration and does not prove production proxy/network behavior.
- Background-worker RED: `carpool-control-browser-3614508758` failed after 25.04s because synchronization never settled without the drain endpoint. After starting `ProvideCarpoolControlWorker`, the same background-mode browser passed in 53.23s (20 checks), artifacts `carpool-control-browser-2001988003`.
- Manual-preview route RED: `carpool-control-browser-3005578470` failed with create-car 404. The earlier run `carpool-control-browser-2744645459` stopped while parsing that empty 404; the API helper was corrected to report the status. After route registration, a smoke input lacking the required cancellation reason failed with 400 (`carpool-control-browser-3597106792`); the test now supplies the real command's required confirmation and reason. No production validation was relaxed.
- Full default backend lint and carpool integration-tag lint both finished with `0 issues.` `git diff --check` passed for tracked changes.

## Final Verification

- Fresh affected PostgreSQL/race: carpool **89.119s**, routes **14.836s**, test-order CLI **2.416s**, all passed. This suite includes historical phases 1-3 transaction/permission/synchronization regressions and phase 4 pricing/subscription/quota tests. Opt-in browser/model tests run separately, not silently counted by this command.
- Focused service/handler/config race: **1.179s / 1.051s / 1.218s**, all passed. The full ordinary service unit suite previously passed in 175.772s at the final 4C gate; phase 4D did not change production service code.
- Fresh production process regression with the rebuilt embedded server: historical model lifecycle **12.72s**, native subscription model lifecycle **8.56s**, both passed. Native artifacts: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-native-model-2986259165`; legacy model artifacts: sibling `carpool-control-model-1052518770`.
- Final native/background-worker browser: **52.59s**, **21 checks**, no page errors. Artifacts: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-browser-2838205814`. Final legacy control browser: **20.91s**, **15 checks**, artifacts: sibling `carpool-control-browser-2257032504`.
- Inspected current desktop native quota, mobile reset window, 320px reset window and admin preparation screenshots. Long car names wrap; dialog bodies scroll; no horizontal overflow or overlapping fixed controls. Some screenshots intentionally show scrolled/cropped body rows under the fixed header, not missing content.
- Synthetic refresh-token-cache fallback and deliberately disconnected-node errors are expected fixtures, not a production readiness claim.

Host binaries are Darwin/arm64 test artifacts, not deployable Linux images. SHA-256:

```text
server                f1a7f8109fbd8b1795336a5f11a341b4935f38695a4711f3f96f068214d11d57
carpool-control.test  216f154b18627f8fa078500a61848bb696e419bbad9192f32a206492d2f9af95
```

- Fresh historical manual browser: **11.14s / 12 checks**, artifacts `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-3396556683`. Legacy order/lobby browser: **12.68s / 18 checks**, sibling `carpool-browser-2412064511`.
- Fresh OIDC lab with the rebuilt server: **16 checks passed**, `results.json` reports `status: passed`, artifacts `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-McMws3`. The preserved-baseline provenance limitation above still applies.

## Retained Local Preview

- Started on 2026-09-23 at approximately 04:40 Asia/Shanghai; automatically ends after eight hours (approximately 12:40 that day), unless stopped earlier or the host exits.
- Administrator: `http://127.0.0.1:65444/__preview/admin`; member: `http://127.0.0.1:65444/__preview/member`. Both were opened and visually verified in the in-app browser. The administrator tab is retained as the deliverable; the role links do not contain tokens.
- PID: `49181`. Stop early with `kill -INT 49181` while that PID still belongs to this preview. This lets its own cleanup remove its temporary containers; do not kill or prune unrelated containers.
- Private runtime/log: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-phase4-preview-6WsXCM`; test artifact directory: sibling `carpool-control-browser-1276587552`.
- The four-person car visibly has 4/4 confirmed seats at 350 CNY each; two-person car 2/2 at 700; three-person car 1/3 at 466.67. B/C connections and internal member mappings are ready. The synthetic OAuth option is selectable and enables initialization. Cars intentionally remain unactivated for the user's own preview interaction; the automated browser/model tests above separately prove activation and usage.
- The member role shows only its own manual receipts and cars, with service not started. All data is synthetic. There is no real model upstream; the `.invalid` API endpoints are not client-ready services. Other admin menus are outside this preview router, and contact details have not been configured. See `tools/carpool/PHASE_4.md` for recreation and limits.
- All finite verification command sessions ended. This explicitly retained preview is the only newly started long-running task left by this delivery.

## Remaining Operational Boundaries

- Real account OAuth/refresh and upstream weekly behavior, SMTP delivery, production domains/TLS, trusted proxy peers and forwarded-header spoofing checks, independent public egress, deployment backup/restore and production load tests remain unverified and require separately authorized operational work.
- The current working tree contains uncommitted/untracked phase 0-4 changes. Git HEAD alone does not identify the tested source. No commit, push, merge, purchase, compliance acceptance, real credential import or image publication has occurred.
- The earlier broader service-race failures remain a separate documented limitation. Affected-path race results must not be described as all-backend race clean.
- Revocation during an A-to-B outage takes effect only after B receives it; already-delivered B subscriptions enforce native expiry/limits locally. This is consistent with the agreed removal of the extra A short lease, not a promise of instantaneous offline revocation.
