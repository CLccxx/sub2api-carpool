# Phase 4B Results

Local verification: 2026-09-22 UTC / 2026-09-23 Asia/Shanghai.

## Delivered

- B supports `CARPOOL_NODE_GROUP_ID=0` for automatic native group bootstrap. The main/node/car/instance identities remain pinned; group ownership is stored durably, never inferred from a name.
- Administrator selects an active OAuth account. Initialization creates or upgrades the empty phase-3 bound group, sets subscription billing, only a weekly limit of `1800/N`, and binds the selected account. Existing delivered groups are not silently repurposed.
- Preparation continues through the existing durable member queue. It does not start service. All confirmed members must be prepared before explicit administrator activation.
- Activation writes one fixed service window, and grant delivery creates each B native subscription with exactly those dates. Retries preserve usage and expiry. Member passwords are not copied and no B member login sessions are created.
- Initialized native subscriptions do not need fifteen-minute A lease renewal. B checks native subscription validity/status, user and group state, plus identity isolation. Old installations keep their legacy gate until explicitly initialized.
- Native cache invalidation follows the transaction commit and repeats after an ambiguous retry. A worker acknowledgements retain existing version/generation protection.
- A can discover a new B instance ID. Pinned origin and generation are checked before sending credentials; blank replacements keep existing encrypted credentials. Added account selector, initialize/confirm-activate controls, common service dates, unfilled-car disabling and collapsed technical identifiers.

## Verification

- Observed RED for missing native bootstrap/activation APIs, frontend instance discovery and activation controls, unfilled-car disabling, stale cross-car account results, and the discovery-before-pinned-origin check. Implemented fixes and verified GREEN.
- Full affected PostgreSQL/race run passed: `./internal/carpool` 83.949s, `./internal/server/routes` 15.306s, `./cmd/carpool-test-order` 2.415s. A prior run failed on a stale SQL mock and one temporary PostgreSQL container startup timeout; neither was counted as a pass. The complete rerun passed without narrowing scope.
- Final connection/native PostgreSQL/race run passed (10.293s), including discovered identity, initialization, immutable term, preserved usage, restart, native expiry/revocation, cache failure/retry, and expired legacy lease independence.
- Handler/service/config carpool unit tests passed. Production server with embedded frontend built. Final Go static analysis: `0 issues.`
- Frontend full run: 293 files / 2322 tests passed; focused API/connection tests, typecheck, lint and production build passed.
- Native browser acceptance passed (22.26s): actual A login and administrator handlers, B/C native group preparation, explicit activation, key lifecycle and response-loss recovery, usage reads, member/car isolation, unavailable node handling, desktop/mobile/320px layouts. Final screenshots inspected; artifacts are in `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-browser-631953231`.
- Legacy phase-3 browser acceptance passed separately (20.71s), artifacts in sibling `carpool-control-browser-1303700182`.
- `git diff --check` passed. No commit, push, resource purchase or production deployment.

## Remaining Boundary

4C is still needed for native subscription/account-window presentation, hourly quota changes and aligned weekly resets. 4D still needs complete native model-request regression and deployment/preview delivery. OAuth fixtures here contain no real upstream credentials. Browser token-cache fallback messages are expected in this isolated harness, and the deliberately stopped C node must return unavailable rather than zero usage.
