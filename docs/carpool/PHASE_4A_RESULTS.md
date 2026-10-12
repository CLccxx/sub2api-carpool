# Phase 4A Results

Local verification date: 2026-09-23 (Asia/Shanghai).

## Delivered

- Nullable car total price with additive migration 011, server-side integer half-up seat pricing and legacy null-total compatibility.
- New drafts default to CNY 1400 total. Two/three/four seats produce CNY 700.00/466.67/350.00. Published terms and existing receipt/refund snapshots remain unchanged.
- Lobby occupancy displays confirmed members / capacity, with remaining seats shown separately.

## Evidence

- RED observed before implementation: eight frontend assertions and two backend assertions failed for the intended missing behavior.
- Backend carpool unit/race passed. PostgreSQL pricing/migration regression passed (7.839s).
- Affected full integration/race: `./internal/carpool` passed (100.778s), `./internal/server/routes` passed (14.658s), `./cmd/carpool-test-order` passed (2.468s).
- Full backend golangci-lint: `0 issues.` Go 1.27.0 Docker runtime with isolated named module/build volumes; lint v2.13.0.
- Frontend: full Vitest run passed, 293 files / 2318 tests. Final focused run passed, 92 tests. Typecheck, lint and production build passed.
- Browser manual enrollment: `CARPOOL_MANUAL_BROWSER=1 ... carpool-browser.test -test.run '^TestCarpoolBrowser$'`, passed (16.89s). Browser legacy test orders: same runner with `CARPOOL_MANUAL_BROWSER=0`, passed (13.62s).
- Manual browser artifacts: `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-3475312643`; legacy artifacts: sibling `carpool-browser-2438579059`. Inspected desktop occupancy, mobile occupancy and mobile default-total screenshots. Assertions cover narrow 320px layout, pricing previews, authorization and no B/balance side effects.
- `git diff --check` passed. No commit, push or production changes.

## Boundary

Only 4A is complete. 4B native subscriptions, 4C quota automation and 4D end-to-end delivery remain. This does not change the existing phase-3 lease gate and does not authorize production deployment.
