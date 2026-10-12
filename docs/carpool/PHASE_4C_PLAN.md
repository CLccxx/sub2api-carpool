# Phase 4C Native Quota And Account Window

**Goal:** Display B's native subscription and upstream weekly window on A; keep per-member limits and weekly resets aligned without a second usage ledger.

**Architecture:** A authenticates the member and selects the already-pinned B/subject. B reads its native account usage service and subscription rows. One B-local minute worker checks the upstream cycle and performs the hourly quota update in the same reconciliation; A's administrator can trigger that reconciliation manually. This implements the required hourly behavior without adding an A availability dependency or a second scheduler. No model request pauses, upstream credit purchases, paid resets or per-request A decisions.

**Tech Stack:** Existing Go native account/subscription services, PostgreSQL synchronization metadata, Vue account console.

## Data Rules

- Initial native group remains `1800/N`. Valid estimate T uses `(T-100)/N`. An invalid/nonpositive/failed/stale estimate never writes zero or removes a native limit. A confirmed new cycle with no estimate uses `1800/N`.
- Read actual source observation timestamps, not `UsageInfo.UpdatedAt` when it is merely the response construction time. OpenAI's expired-window renderer can synthesize zero; an expired reset timestamp is not a fresh zero sample.
- Persist only source snapshot/cycle/last-sync metadata on B. Usage, limits and expiry remain the native tables. Member DTOs omit account credentials, administrator identifiers and other users' data.
- Serialize quota/reset changes per B. Older delayed source reads cannot overwrite newer snapshots. Native subscription row locks and conditional window advancement protect concurrent charging.
- Initial alignment changes the native weekly anchor without clearing current usage. On a verified subsequent cycle, clear only rows whose native weekly anchor has not already advanced; native maintenance may have reset them first. The same cycle can never clear later usage again.
- Cycle identity follows whole seven-day advances. Timestamp corrections up to five minutes align anchors without creating an extra cycle; larger unexplained deviations report unavailable and preserve existing native values. This tolerance is an application safety policy, not an upstream guarantee. Already-advanced native rows keep their charges, including after several missed cycles.
- OpenAI stores a coherent `codex_7d_observation` tuple alongside native account metadata. Percentage, reset and observation time come from one complete weekly response. Five-hour-only/partial updates leave that tuple unchanged; the reader requests native account cost for that exact window. Legacy snapshots without the tuple remain unavailable until a genuine complete response arrives.
- Native seven-day maintenance must honor an explicitly account-aligned anchor, including midnight anchors, rather than applying the legacy first-day heuristic. Do not change ordinary A subscription behavior.
- Manual sync and automatic sync use the same cycle check. This is a recovery action, not an arbitrary repeated current-week wipe.
- Expose snapshot age/error states. Missing data is not zero. API base URL formatting follows the native group's platform.

## Ordered Tasks

- [x] RED: quota formula, unknown/zero/invalid/stale snapshot, monotonic-cycle and response-order tests in `native_quota_test.go`.
- [x] GREEN: metadata migration 013, native B snapshot/update/read methods and transactionally idempotent weekly alignment in `native_quota.go`; test real PostgreSQL consumption preservation and cycle replay.
- [x] RED/GREEN: `CarpoolQuotaWorker` adapter to native `AccountUsageService`, source timestamps and lifecycle wiring; no credentials in public DTOs or logs.
- [x] RED/GREEN: align managed B native subscription maintenance with account anchors; preserve ordinary subscription behavior and cache invalidation after commit/retry.
- [x] RED/GREEN: bound B member subscription read and administrator refresh endpoints, scoped A proxy methods/routes and account-specific API address.
- [x] RED/GREEN: quota tab with native weekly used/limit/remaining, account utilization, estimate, source time and reset time; administrator one-click sync, stale/error/empty/expired states, late-response isolation.
- [x] Review corrections: reproduce same-week/+2 minute double-reset errors, separate coherent weekly freshness from the shared account timestamp, and propagate authentication-cache lookup/delete/publication errors through native synchronization.
- [x] Verify 450 -> 475 with unchanged usage, decreasing limit, one reset per cycle, in-flight charge ordering, restart/retry, missing/fabricated source data and member isolation. Run affected Go/race, frontend and desktop/mobile browser tests before 4D. Final evidence and review corrections are recorded in `PHASE_4C_RESULTS.md`.

Production source observations require the selected OAuth account to expose a genuine weekly window. A lack of fresh native data is surfaced as unavailable and retains the last committed native limit; it is not replaced with invented budget/usage figures.
