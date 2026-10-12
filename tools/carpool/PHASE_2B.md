# Phase 2B: Manual Enrollment UI

This local test workflow uses fictional accounts and receipt records. It does
not transfer money, verify payment channels, send email, activate service, grant
node access, or provision resources. Backend configuration and contracts remain
in `PHASE_2A.md`; the approved user workflow is in
`docs/carpool/PHASE_2_MANUAL_DESIGN.md`.

## Administrator Workflow

1. Configure the existing site contact text. Publish a manual vehicle with a
   fixed seat price, capacity, recruitment deadline and delivery window.
2. Verify the off-platform receipt yourself. Use the vehicle's add-member action
   to search an existing active account, select its email and ID, and enter the
   channel, actual receipt time and optional reference/internal note.
3. Acknowledge receipt, review identity/amount/vehicle, then confirm enrollment.
   Capacity is checked again by the server. A full car awaits deployment only.
4. Use the manual-orders tab to inspect records, withdraw members with a reason,
   or process the pending-refund queue. Withdrawal/cancellation never claims
   money was refunded. After completing the actual off-platform full refund,
   explicitly record it with its time and optional reference.

Disabling new manual enrollment retains historical reads and refund operations.
A full car that loses a member needs administrator review; it does not reopen
recruitment or reset its delivery promise. Ordinary users have contact/history
views, not reservation, payment or cancellation controls for manual records.

## Unknown Results and Privacy

An uncertain mutation retains the original actor, action, target, full body and
idempotency key in this tab's `sessionStorage`, with an in-memory fallback. This
includes an optional reference/internal note, so use a trusted browser profile.
No recovery payload is saved in `localStorage`. Reloading or returning to the
page offers the same-request retry; a later conflict does not prove the first
attempt failed. Inspect the administrator order list before explicitly ending
reconciliation. Do not start a new registration merely because a response was
lost, especially when no external reference was recorded.

Logout and terminal authentication expiry clear manual recovery data, including
when the carpool view is unmounted. Transient refresh failures retain it. Old
responses cannot erase a newer request or restore a cleared journal. After a
logout, a closed tab, or blocked storage plus reload, use server order history
to reconcile; this local journal is not a durable accounting ledger.

## Frontend Verification

From `frontend/`:

```sh
pnpm test:run
pnpm lint:check
pnpm build
```

`build` includes locale-key checks and `vue-tsc -b` before Vite. No new application
dependency is required. API/helper/view/composable/auth tests cover user search
races, exact amounts, date bounds, immutable retries, actor isolation, logout
cleanup and mode-specific controls.

## Browser Regression

Prepare the frontend build, Go 1.27.0, Docker, local Chrome, and the dependencies
under `tools/carpool`. Build a host-platform integration test runner and CLI:

```sh
cd backend
go test -mod=readonly -c -tags=integration -o /tmp/carpool-2b-bin/carpool-browser.test ./internal/server/routes
go build -mod=readonly -o /tmp/carpool-2b-bin/carpool-test-order ./cmd/carpool-test-order
cd ..
CARPOOL_BROWSER_TEST=1 CARPOOL_MANUAL_BROWSER=1 \
CARPOOL_BROWSER_CLI=/tmp/carpool-2b-bin/carpool-test-order \
CI=true /tmp/carpool-2b-bin/carpool-browser.test \
-test.v -test.run '^TestCarpoolBrowser$' -test.timeout=7m
```

The parent directory for the binaries must already exist. When cross-compiling
in Docker for macOS, mount the repository at its exact absolute host path so
`runtime.Caller` in the runner locates the host frontend and scripts. Use
`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0` for Apple Silicon. Point `DOCKER_HOST` at
the host's Docker socket when necessary. See `PHASE_2A.md` for cache mounts and
the Linux-container PostgreSQL/race commands.

The manual run covers desktop English, mobile Chinese, 320px long terms, real
PostgreSQL/JWT transactions, lost committed responses plus reload, withdrawal,
refund registration with additions disabled, and cancelled-car refunds. User
search and unrelated UI dependencies are authenticated synthetic fixtures, not
production implementations. Search-response reordering is covered in Vitest.

Repeat with `CARPOOL_MANUAL_BROWSER=0` for the legacy 18-group browser suite.
Both modes use isolated PostgreSQL and loopback HTTP; external browser requests
are blocked. The positive harness omits compliance middleware without accepting
any declaration. Run the production compliance/auth route tests separately.
Never copy preview authentication or fixture endpoints into production.

## A/B Isolation

Build an embedded current server after the final frontend build. Preserve an
embedded original server from commit
`881f3202694c6bc932446931a30c27d9675178b9` in a separate source tree; do not build
the baseline from modified code. Then run:

```sh
CARPOOL_BASELINE_BINARY=/tmp/carpool-2b-bin/baseline-server \
CARPOOL_TEST_BINARY=/tmp/carpool-2b-bin/manual-server \
CARPOOL_MEMBERSHIP_CLI=/tmp/carpool-2b-bin/carpool-membership \
node tools/carpool/lab.mjs
```

The membership CLI must also be built for the host. `README.md` describes lab
isolation, OIDC fixtures, cleanup and network limitations. This is not a
production egress sandbox or proof of independent public IPs/TLS.

## Interactive Preview

Add `CARPOOL_BROWSER_PREVIEW=1` to the manual browser command and use
`-test.timeout=9h`. It prints loopback-only administrator/user URLs with
`preview_user=1/2`; URLs contain no token. It seeds 2/3/4-seat synthetic cars and
two fictional members, and stops after eight hours. Send SIGINT to the recorded
runner PID to stop early and allow cleanup. Roles share one origin, so use
separate browser profiles for simultaneous administrator/member inspection.

Keep private runtime directories out of Git and public uploads: test logs or
configuration may contain local credentials. `results.json` and screenshots
contain fictional acceptance data only. Ports, PIDs and artifact paths are
run-specific; measured results are in `docs/carpool/PHASE_2B_RESULTS.md`.
