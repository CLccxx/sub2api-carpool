# Phase 4B Native Subscription Initialization

**Goal:** Prepare the B native group and account, then explicitly activate a full car once, without password synchronization or renewable access leases.

**Architecture:** Keep the existing durable A-to-B member/grant queue. Add a durable initialization record on each side. B creates its native group and binds exactly the selected OAuth account in one transaction; grant delivery creates each native subscription with A's fixed service dates. Native subscription status and validity replace the extra short lease only on initialized cars. Legacy installations retain their old behavior until explicitly initialized.

**Tech Stack:** Existing Go, PostgreSQL, Vue and A/B test harness.

## Sequence

- [x] RED: real migrated B database test for group auto-creation, 450 initial quota, account binding, immutable retries, prepared denial, active native subscription, expired lease independence, native expiry/revocation and group isolation.
- [x] Add migration 012 for native setup records and optional native grant flag; keep existing migrations intact.
- [x] Permit group_id=0 for auto-bootstrap; validate immutable main/node/car/instance binding and refuse attaching unrelated existing users/groups/subscriptions.
- [x] Add B initialization endpoint; validate active OAuth account and assign an exclusive native subscription group with no daily/monthly limits. Record stable group ID in the same transaction.
- [x] Apply native grants transactionally to native subscriptions. Preserve dates/usage on retries. Invalidate native caches after commit, including when retrying an already-committed grant.
- [x] Add A preparation and explicit activation endpoints. Persist initialization intent before HTTP, check generation on completion, require all current confirmed members prepared before activation, and write one common service window plus durable grants atomically.
- [x] Simplify first connection by discovering and pinning B instance ID on the server after checking configured main/node/car identity. Preserve existing pinned identity and origin protections.
- [x] Add administrator account selection, initialize and confirm-activate UI using step-up handling; keep all secrets out of member DTOs.
- [x] Verify full affected integration/race, unit, frontend, build and actual native A/B HTTP workflows before 4C.

## Safety Decisions

Group initialization does not start a subscription. Activation commits fixed dates once; retries never extend them. The group ID is durable, not rediscovered by a mutable display name. An initialized node accepts only native grants and does not use its old short lease for request eligibility. Native subscription loss, expiry, suspension, disabled users and cross-group keys remain denied. A downtime cannot renew service or stop a valid B subscription. No code changes to model streaming or a second usage ledger are required.
