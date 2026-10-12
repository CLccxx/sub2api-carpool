# Phase 1B Local UI Validation

This is local test tooling, not a deployment guide. No payment, SMTP, real upstream
account, node activation or administrator compliance acknowledgement is involved.

## Application Pages

- `/admin/carpool`: vehicles, draft form, publish/cancel confirmation and members.
- `/carpool`: authenticated catalog, rules and test-seat reservations.
- `/carpool/mine`: owned test orders, hold countdown, cancellation and waiting members.

All three routes and navigation entries require public `carpool_main_enabled=true`.
The public API and SSR derive it from `carpool_main.enabled && !carpool_node.enabled`.
`carpool_test_orders_enabled` additionally requires the test-order configuration.
Neither flag can be changed through database/admin settings. Both default off.
Closing test orders preserves catalog/admin access and existing hold cancellation.

The backend remains authoritative. UI countdowns use a monotonic server-time sample
and conservatively account for request latency. Reservation attempts are stored in
session storage by local user/car, with memory fallback when storage is unavailable.
Uncertain retries reuse a key; observed terminal orders retire only their matching
known attempt. No user ID, amount, seat number or confirmation state is supplied by
the reservation request.

Admin creation is not an idempotent API. A network/5xx failure therefore blocks
resubmission until an explicit, successfully accepted refresh of the first draft
page. The operator must inspect the list before deliberately starting another draft.

## Build and Test

Prerequisites: compatible Go, Node, pnpm, Docker and local Chrome. Use the temporary
toolchain documented in the results if the system Go is older than the project.
`tools/carpool/node_modules` uses the existing lab's pg/Playwright packages.

From `frontend/`:

```sh
pnpm run test:run
pnpm run lint:check
pnpm run build
```

From `backend/`:

```sh
go build -o /tmp/carpool-phase1b-test-order ./cmd/carpool-test-order
CARPOOL_BROWSER_TEST=1 CI=true go test -v -count=1 -tags=integration \
  ./internal/server/routes -run '^TestCarpoolBrowser$'
go test -race -count=1 -tags=unit ./internal/service ./internal/handler \
  ./internal/handler/dto ./internal/server/routes \
  -run 'Carpool|PublicSettingsInjectionPayload'
CI=true go test -race -count=1 -tags=integration ./internal/carpool \
  ./internal/server/routes ./cmd/carpool-test-order
```

The browser test skips unless explicitly enabled. It creates a private PostgreSQL
container, applies real migrations, signs real JWTs and runs real carpool handlers
and transactions. Static files come from the compiled frontend. Core authentication
middleware is real; identity lookup and unrelated shell endpoints use synthetic
fixtures. Four loopback servers represent main, read-only, disabled and node
visibility. Node admission itself is covered by the separate A/B lab.

The positive admin test harness intentionally excludes compliance middleware, as
in the phase-1A HTTP test; it is never included in the production executable. Its
`/admin/compliance` response is a test fixture, with no acceptance endpoint or
acknowledgement persisted. `TestCarpoolAdminComplianceAndAudit` separately asserts
the production registration returns 423. This is not production compliance UAT.

The Node runner receives synthetic credentials on stdin, not command-line args.
It only permits browser network requests to localhost. PostgreSQL and HTTP servers
are cleaned on completion. Screenshots and a sanitized `results.json` remain in the
printed mode-0700 temporary artifact directory; do not publish raw diagnostic logs.
`CARPOOL_BROWSER_CHANNEL` can select another locally installed Playwright channel;
Chrome is the default. No automatic browser downloads.

## Interactive Test Preview

From `backend/`, after building frontend and CLI:

```sh
CARPOOL_BROWSER_TEST=1 CARPOOL_BROWSER_PREVIEW=1 go test -v -count=1 \
  -timeout=9h -tags=integration ./internal/server/routes -run '^TestCarpoolBrowser$'
```

This opt-in integration-test mode prints loopback admin/user preview links and
creates three synthetic 2/3/4-seat cars; the two-seat example is test-confirmed and
waiting for deployment. Opening a link initializes a synthetic local session.
Links on the same origin switch that browser's identity. No passwords, DSNs or JWTs
are included in the links. The test-only initialization route cannot exist in the
production server because the entire harness is integration-tagged test code.

Preview resources intentionally stay running for interaction and auto-stop after
8 hours; Ctrl+C stops earlier. Normal browser validation does not leave them running.
This preview is not a real deployment and must never be bound to a public interface.
