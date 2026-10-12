# Phase 4C Results

Status: 4C local acceptance complete on 2026-09-23 (Asia/Shanghai). The first review's three correctness findings were reproduced, fixed, independently reviewed and verified in expanded regressions. Phase 4D is next; the overall phase-4 goal remains active. Results preceding the final-verification section describe the earlier revision and are retained for provenance.

## Delivered

- B owns one minute reconciliation worker. It reads the selected native OAuth account's genuine seven-day snapshot and performs hourly group-limit updates in the same job. An administrator can request the same reconciliation through A, with step-up protection.
- Initial/new-cycle fallback remains `1800/N`; a valid estimate T changes the existing native group to `(T-100)/N`. Four members start at 450 and T=2000 yields 475. Adjustments preserve native usage; invalid/stale/failed inputs cannot write an unlimited/zero limit.
- Migration 013 stores only source times, upstream cycle and synchronization/cache acknowledgements. Native subscriptions remain the only usage and expiry records. Source timestamps are normalized to PostgreSQL precision; delayed observations are rejected.
- Initial alignment preserves consumption. Later cycles conditionally advance native weekly windows once, preserving charges already recorded after native maintenance, including header clock jitter. Daily/monthly usage and expiry are not cleared by this reconciliation.
- Newly activated members use an already-discovered account anchor. Managed B's native subscription maintenance recognizes that anchor, including midnight, without changing ordinary A subscription behavior.
- Cache invalidation is acknowledged durably after commit and retried after failure/restart. No A quota gate, model proxy, short-lease renewal or request pause was introduced.
- A reads a member-scoped native subscription snapshot from B, with native usage/limit/remaining and sanitized account metadata. Missing/null response fields are rejected rather than decoded as zero. API addresses follow the native platform: OpenAI uses `/v1`, Anthropic uses the origin.
- My Car includes subscription quota, account utilization, estimate, source timestamps and account reset time/countdown. Administrator connection settings include manual quota/cycle synchronization. Failed, stale, uninitialized and genuinely zero observations are distinct; late responses cannot cross car/user changes.

## Verification

- Observed RED and then GREEN for timestamp replay precision, native-reset clock jitter, failed-group-update rollback, activation after initial alignment, authoritative midnight anchors, API/route permissions, malformed/missing native data and quota UI.
- Complete affected PostgreSQL/race: carpool 88.471s, routes 15.171s, test-order CLI 2.430s. This includes an actual PostgreSQL row-lock wait: a request advances its window and commits a new charge while reconciliation waits; that new charge survives.
- Related service/handler/config unit/race: 4.344s / 1.536s / 1.259s. Ordinary subscription reset/progress rules and new B policy were covered together.
- Protocol and caller-supplied-identity checks passed under race after the final validation change: carpool 1.028s, handler 1.059s.
- Final native quota/subscription/A-B HTTP PostgreSQL/race rerun passed in 14.329s after strict response validation; final frontend lint/typecheck passed again after countdown changes.
- Frontend full run: 293 files / 2327 tests. Final focused quota/API/connection run: 45 tests. Typecheck, locale checks, lint and production frontend builds passed.
- Full Go lint: `0 issues.` Embedded production server compiled for the host. No commit, push, real upstream authorization or production deployment.
- Native A/B/C browser acceptance passed in 23.50s; legacy mode passed separately in 20.13s. Native coverage includes manual reconciliation through the real worker/account reader, genuine zero vs missing estimate, member/car isolation and desktop/mobile/320px layouts. Final screenshot paths will be recorded after inspection.

## Failures Retained

- Earlier native browser runs stopped on second-car readiness. The fixture assumed `SyncNext=false` meant the queue was empty, but `SKIP LOCKED` can skip a temporarily locked connection while the UI reads status. The test fixture now waits on actual pending delivery state; the production worker already polls continuously. Assertions were not weakened.
- Two concurrent host browser processes collided in Testcontainers reaper startup. They did not reach application tests. Serial reruns passed; do not run these host browser fixtures concurrently against this Docker environment.
- Initial lint found two unchecked type assertions in the protocol test. Both were fixed and the full rerun passed.
- Fixture refresh-token-cache warnings and intentionally disconnected-node errors are expected. They do not prove production token-cache or network readiness.

## Review Corrections

- P1 reset deduplication: reproduced a two-minute authoritative timestamp correction clearing already-charged native usage. Cycle classification now uses whole seven-day advances with a five-minute correction tolerance; it aligns already-reset rows without clearing their consumption. PostgreSQL/race quota tests passed in 8.490s, covering same-cycle corrections, one/three-week gaps, native reset/charging, replay and row-lock interleaving. Larger unexplained deviations must be visible as unavailable, not silently accepted as a new cycle.
- P1 weekly freshness: reproduced shared `codex_usage_updated_at` accepting missing/incomplete weekly data. Both native OpenAI snapshot writers now persist a complete weekly tuple atomically; partial/five-hour-only updates cannot freshen or mix it. The carpool reader obtains native account cost for the tuple's window. Source/writer/worker focused race tests passed in 1.805s; a further reader-window regression has been added for the expanded run.
- P2 cache acknowledgement: reproduced swallowed group-key lookup, Redis delete and publication errors through the actual native provider callback. A checked path now propagates these errors while preserving ordinary callers' best-effort behavior. Focused race checks passed in 1.493s. Additional PostgreSQL tests exercise durable pending revisions, node reconstruction and successful retry without replaying usage/reset writes.
- An additional RED check found an ambiguous cycle was not recorded as an unavailable source. The worker now handles this conflict consistently with invalid observations; its GREEN rerun belongs to the expanded verification below.

## Final Verification After Corrections

- Full affected PostgreSQL/race: carpool 98.813s, routes 14.821s, test-order CLI 2.249s. This includes the new real-provider durable authentication-cache retry tests and cycle-correction tests.
- Focused service/handler/config race: 2.349s / 1.191s / 1.238s, including the coherent-window reader and ambiguous-cycle status regression. Full service ordinary unit suite passed in 175.772s. The previously documented broader service-race limitation is not claimed resolved.
- Frontend full suite: 293 files / 2327 tests; focused carpool run: 129 tests. Production build, locale checks, lint and typecheck passed. Existing Vue test stubs, Browserslist and bundle-size warnings remain; none were counted as a clean-warning claim.
- Default full-backend lint and carpool integration-tag lint both ended at `0 issues.` The integration run initially found an unchecked type assertion in the earlier connection test; it now has an explicit assertion, and that PostgreSQL/race test passed again in 2.832s.
- Host browser runner and embedded production server rebuilt. Native desktop/mobile/320px acceptance passed in 27.87s; legacy mode passed serially in 19.86s. Inspected desktop/mobile account-window screenshots and the narrow subscription view, with no overlapping controls or clipped content.
- Native artifacts: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-browser-1654451804`; legacy artifacts: sibling `carpool-control-browser-2159113325`. Synthetic login-cache fallback and deliberately disconnected-node errors are expected fixture states.
- Independent read-only follow-up review found the original three issues resolved and no concrete counterexample. This complements, not replaces, executed verification. No commit, push, real OAuth authorization, purchase or production deployment.

## Remaining Boundary

4D must still verify the complete native model-request/billing path, A downtime, expiry/revocation, delivery instructions and a fresh local preview. Real OAuth data, production TLS/DNS/egress and actual server purchases remain separately authorized operational checks. Do not treat synthetic fixtures as proof of those production conditions.
