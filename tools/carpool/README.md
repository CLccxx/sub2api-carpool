# Phase 0 Local Authentication Lab

This is a disposable verification lab, not deployment tooling. It uses synthetic
users, an OIDC test realm and a fake model upstream. It never purchases servers,
connects real payments, or imports real upstream credentials.

## Prerequisites

- Docker Engine/Desktop running; permission to create disposable containers.
- Go matching `backend/go.mod`, Node 20, pnpm, and installed Google Chrome.
- Available images: `postgres:18.1-alpine3.23`, `redis:8.4-alpine`,
  `quay.io/keycloak/keycloak:26.7.3` (Docker downloads missing images).
- A preserved embedded original binary from commit
  `881f3202694c6bc932446931a30c27d9675178b9`.

Do not build the baseline from the modified working tree. In a separate clean
checkout of that commit, install/build the frontend and build the embedded
backend with the commands below, changing the output to an absolute baseline
path. The current run preserved it at `/tmp/carpool-baseline-server`.

## Build and Run

From the repository root:

```sh
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend run build
cd backend
CGO_ENABLED=0 go build -tags embed -trimpath -o bin/server-embed ./cmd/server
go build -o bin/carpool-membership ./cmd/carpool-membership
cd ..
pnpm --dir tools/carpool install --frozen-lockfile
CARPOOL_BASELINE_BINARY=/absolute/path/to/original-server node tools/carpool/lab.mjs
```

`CARPOOL_TEST_BINARY` optionally selects a different modified server binary.
The CLI path is `backend/bin/carpool-membership`. The lab reports its private
runtime directory and prints sanitized check results. `results.json` contains
the machine-readable summary; screenshots contain only synthetic user data.
Other logs/configuration contain generated credentials: do not commit, publish,
or upload the runtime directory.

Published ports bind `127.0.0.1`; A/B use distinct `a.localhost`/`b.localhost`
browser origins, databases, Redis instances, signing keys and OIDC clients.
The IdP issuer is an ephemeral loopback URL. The lab cleans only its own named
containers/processes on normal completion or caught failure and retains private
logs. A forced kill may require operator cleanup of containers labeled
`carpool.phase0`; never remove unrelated containers.

Model and pricing calls use the local stub. Browser external requests are
blocked. This is **not a network-egress sandbox**: original backend background
workers can still contact public metadata services. No real model credentials
are provided. HTTP/loopback results do not prove TLS or independent public IPs.

## Opt-In Node Configuration

The application defaults to the original behavior. B opts in using:

```text
CARPOOL_NODE_ENABLED=true
CARPOOL_NODE_NODE_ID=node-b
CARPOOL_NODE_CAR_ID=car-1
CARPOOL_NODE_ISSUER=<exact OIDC issuer>
RUN_MODE=standard
OIDC_CONNECT_ENABLED=true
OIDC_CONNECT_USE_PKCE=true
OIDC_CONNECT_VALIDATE_ID_TOKEN=true
OIDC_CONNECT_ISSUER_URL=<same exact OIDC issuer>
```

Also configure the usual OIDC client, secret and redirect settings. The lab
requires verified email. Runtime OIDC settings cannot disable PKCE/token
validation or change the configured node issuer. The gate uses a separate
checksum-tracked migration stream, not edits to upstream migrations.

Each new protected request queries PostgreSQL for the node/car/issuer/subject
membership, active user, service window and lease. There is no membership allow
cache. Failures return `NODE_MEMBERSHIP_REQUIRED` (403) or
`NODE_MEMBERSHIP_UNAVAILABLE` (503). This favors immediate revocation over query
throughput; load testing and production capacity planning are still required.
Existing in-flight streams can complete; requests admitted after revocation
are denied. Dedicated administrator console login is retained, but model API
keys owned by administrators do not bypass membership.

## Manual Grant Fixture

The CLI uses different environment names from the server:

```sh
CARPOOL_DATABASE_DSN="$LAB_B_DSN" \
CARPOOL_NODE_ID=node-b CARPOOL_CAR_ID=car-1 CARPOOL_ISSUER="$LAB_ISSUER" \
backend/bin/carpool-membership < /private/path/grant.json
```

JSON shape (replace placeholders with real lab values and RFC3339 timestamps):

```json
{
  "node_id": "node-b",
  "car_id": "car-1",
  "issuer": "<lab issuer>",
  "subject": "<verified IdP subject, not email>",
  "version": 1,
  "status": "active",
  "valid_from": "<start>",
  "valid_until": "<service end>",
  "lease_until": "<lease end>"
}
```

Statuses: `active`, `paused`, `revoked`. Service end must follow start; lease
must not exceed service end or current time plus 15 minutes. Every changed
grant/renewal/revocation requires a strictly higher version. An identical replay
is idempotent; stale or conflicting versions fail. Unknown JSON fields fail.
This is a privileged local operator fixture, **not a network synchronization
API**. Do not expose it to a browser or hand users database credentials.

## Verification

From `backend/`: `go test -tags=unit -count=1 ./...`,
`CI=true go test -tags=integration -count=1 ./...`, and
`golangci-lint run --timeout=10m ./...`.
From the root: `make test-frontend`.
Integration tests require Docker; `CI=true` prevents the upstream repository
harness from silently accepting an unavailable Docker service.

See `docs/carpool/PHASE_0_AUTH_RESULTS.md` for results and remaining limitations.
