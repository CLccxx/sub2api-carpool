# Phase 3C Local Verification

Date: 2026-09-19 (Asia/Shanghai).

The administrator and member interfaces, sensitive-data lifecycle, native A/B/C
browser integration and production B/C model-gateway acceptance are implemented.
The overall phase audit and final backend checks are recorded separately in
`PHASE_3_RESULTS.md`. Nothing here activates production service or configures SMTP
or payments.

## Delivered

- `CarpoolNodeConnection.vue` is available from the administrator vehicle row
  only when the config-derived control flag is enabled. It covers registration,
  fixed identifiers, masked credential readback, generation-aware saves,
  connection checks, disable/enable, paginated synchronization and explicit retry.
- Writes use `useStepUp` and `TotpStepUpDialog`. Closing, logout, feature changes
  and car/actor changes invalidate responses and verification replays and clear
  credentials. A late STEP_UP_REQUIRED cannot reopen a dismissed dialog.
- An uncertain save blocks further writes until a successful reload obtains the
  current generation. A blank replacement preserves the existing credential.
- `CarpoolNodeConsole.vue` opens from a membership in My cars. It has scoped
  key creation/recovery, rename, enable/disable, confirmed deletion, API URL,
  service dates and paginated UTC usage queries (up to 31 inclusive days).
- The component distinguishes unactivated, synchronizing, unavailable, paused,
  revoked and expired access. Failed usage queries clear statistics, rather than
  display zero. Existing A-native key/usage pages are unchanged.
- Full keys appear only after creation or legitimate replay, in component memory.
  They are discarded on close, tab/car/actor change, logout and unmount. Lists
  retain only metadata even if a response unexpectedly contains a full key.
- `carpoolKeyJournal.ts` stores actor/car/name/operation only, recovers the same
  operation after reload, and has auth-store and terminal-401 cleanup that also
  works after the page unmounts. A deleted-key replay ends instead of recreating it.
- Service badges, dates and administrator filters now support the new states.
  Chinese and English keys have matching schemas. The lobby layout is unchanged.

## Observed Regression Fix

The first native browser run exposed an integration mismatch: the common Axios
interceptor injected `timezone` into every GET, while control endpoints reject
unknown query fields. Six failing interceptor tests reproduced the extra field.
Carpool requests now preserve their explicit query contract; other APIs still
receive their normal timezone parameter. Both API tests and native browser
replay passed after the fix.

A separate early harness-only login failure came from a JWT-only fixture lacking
an ent client. The native auth fixture now receives its actual database client;
no production login implementation was changed for this failure.

## Evidence

| Verification | Result |
| --- | --- |
| Full frontend Vitest | 293 files / 2311 tests passed; 21.26s |
| Component regression | 13 connection and 17 console tests passed |
| Journal/auth/interceptor cleanup | Passed, including unmounted journals and refresh-failure cleanup |
| Typecheck and production build | Passed; locale parity and `vue-tsc -b` included |
| Frontend lint | Passed in a standalone run; a prior concurrent scan hit a deleted Vite temporary file |
| Native A/B/C browser | 14 groups passed; final replay 21.99s |
| Complete production B/C model processes | Passed; final replay 11.82s, including in-flight stream completion |
| Native A/B/C HTTP contracts | Passed; final replay 6.03s |
| Final stable-subject concurrency replay | Passed; 1.51s |
| Integration-tag carpool lint | 0 issues after test formatting and a documented trusted-executable annotation |
| Historical manual browser | All 12 groups passed; 12.69s |
| Historical test-order/lobby browser | All 18 groups passed; 16.14s |
| Historical OIDC A/B | All 16 groups passed with current embedded server and CLI |

Frontend test log: `/tmp/carpool-phase3c-frontend-tests.log`.

Final native browser screenshots and results:
`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-browser-1914787774`.
Desktop 1440px English, mobile 390px Chinese and narrow 320px English passed DOM
overflow checks. Viewport screenshots were inspected, including long titles,
masked credentials, keys and date/usage forms. Screenshots never capture a full
key. Browser external requests were blocked.

Final managed model logs:
`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-model-2226517076`.
The test uses complete production node binaries, independent PostgreSQL/Redis,
native API-key authentication and forwarding, and a local OpenAI-compatible
upstream. A queries usage written by the real model requests; these records are
not inserted directly. Assertions cover B/C key separation, zero preparation
balance, denied node member login, warmed-key revoke, pause, service end, local
lease expiry and node/control-store restart. A stream admitted before revoke is
allowed to complete while new requests are rejected without reaching upstream.

Historical artifacts:
- Manual: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-490345909`
- Legacy: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-1856153392`
- OIDC: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-5QGH5L/results.json`

## Test Boundaries

The control API positive fixtures use native handlers, repositories, JWT and
administrator-key authentication, but omit legal-compliance acknowledgement.
No declaration was accepted; production compliance denial is tested separately.
The browser fixture exercises real password login without a Redis refresh-token
cache, so its expected access-token fallback is not a refresh-session acceptance
test. The historical OIDC suite covers the existing refresh/revocation path.

Active service dates and billing balances are introduced only by isolated test
SQL. Normal enrollment, connection checks and preparation never activate cars,
grant subscriptions or credit wallets. Local TLS/transport fixtures do not prove
production public egress or production certificates. No real credentials,
payments, SMTP, purchases, remote pushes or deployments were used.

Existing Browserslist/bundle warnings and the broader unrelated service race
failures remain documented in `PHASE_3B_PROGRESS.md`; this is not a claim that the
entire upstream backend is race-clean. Temporary artifact paths may be removed
by the OS. Reproduction instructions: `tools/carpool/PHASE_3C.md`.
