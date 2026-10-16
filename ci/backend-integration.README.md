# Disposable backend integration CI

`.github/workflows/backend-integration.yml` runs on a new GitHub-hosted Ubuntu
runner. It never connects to the developer fixture, a deployed tenant, or a
production endpoint. The script refuses ordinary local execution.

## What actually runs

The eight packages in `backend-integration.json` run one at a time, using Go
1.27.2, `GOWORK=off`, `GOTOOLCHAIN=local`, `-mod=readonly`, `-race`, `-count=1`,
`-p=1`, and `-parallel=1`. Individual tests still exercise their intended
concurrency. `go vet` runs for each package even if that package's tests fail.

The contract names **51 mandatory integration top-level tests**. Missing or
skipped mandatory tests, skipped descendants, malformed/empty JSON, race/test
failures, timeouts, and vet failures all reject the job. Other skipped tests are
listed and counted as skips, never as integration passes.

| Package | Mandatory integration tests | Boundary |
| --- | ---: | --- |
| `internal/meetingbooking` | 8 | Real RS transactions, owner/tenant isolation, stable receipt/CAS/cancel, explicit index readiness |
| `internal/favorite` | 3 | Twelve persisted contracts, concurrent category operations and real rollback |
| `internal/retention` | 13 | Policy-document conflicts, off/extend/ABA, owner floors, transactional outbox and mixed writers |
| `pkg/common/db/mongotx` | 1 | Request-specific transaction context, cancellation, rollback and concurrency |
| `internal/api` | 6 | In-process HTTP with real live-token Redis status, owner rejection, booking and service proofs |
| `pkg/common/db/controller` | 8 | Real message deletion/visibility/receipts/identity with Mongo and Redis |
| `pkg/common/db/mgo` | 9 | Random isolated DBs for commands, membership, bootstrap, proof/CAS, nickname indexes and owner metadata |
| `pkg/common/db/cache` | 3 | Version allocation, exact token revocation and monotonic int64 owner/global floors |

These are existing tests and assertions. No product source or assertion is
replaced for this workflow. API tests are `httptest` handler tests, not deployed
HTTP/WSS, browser, or native acceptance. Message-controller tests do not launch
Kafka, ZooKeeper, a gateway, a cron process, or real message delivery.

## Exact fixture environment

| Variable | Value |
| --- | --- |
| `LUOBO_INTEGRATION_MONGO_URI` | `mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration` |
| `LUOBO_BOOKING_TEST_MONGO_URI` | Same exact Mongo URI |
| `LUOBO_RETENTION_TEST_MONGO` | Same exact Mongo URI |
| `LUOBO_MONGO_TEST_URI` | Same exact Mongo URI |
| `LUOBO_BOOKING_TEST_REDIS_ADDR` | `127.0.0.1:36379` (test selects DB 15) |
| `LUOBO_RETENTION_TEST_REDIS` | `redis://127.0.0.1:36379/15` |
| `LUOBO_INTEGRATION_REDIS_SOCKET` | Fresh `/tmp/luobo-it-RUN-ATTEMPT/references/integration/redis8.2/redis.sock` |

The socket suffix preserves the existing helpers' safety checks. Mongo and
Redis use host networking but bind **only 127.0.0.1**, on the existing fixtures'
nondefault ports. The single replica-set member advertises that same loopback
Mongo address; a writable-primary `hello` gate is required before any tests.
This is an isolated container without imported users/data or authentication
secrets, not a database IAM or production TLS verification.

Container names carry the exact run/attempt label; setup rejects existing names
and paths, and never resumes or retries an unknown `rs.initiate` result. Mongo
data is a bounded empty tmpfs. Redis persists neither RDB nor AOF. The only host
bind mount is the fresh private Unix-socket directory, with the runner UID/GID.
No workspace data or configuration is mounted into either database.

Tests create random `luobo_booking_test_*`, `luobo_retention_test_*`,
`luobo_favorite_test_*`, `luobo_tx_request_*`, `luobo_commands_test_*`,
`luobo_receipts_test_*` and other fixture-prefixed databases. Booking/retention
helpers exclusively claim a fresh database before granting cleanup authority;
their cleanup errors fail tests. Booking Redis and retention Redis reserve
three nonce-qualified keys atomically and check their ownership on cleanup.
Some older helpers have weaker cleanup error reporting. A separate post-test
audit requires zero non-system Mongo databases. Redis may retain intentional
client-message locator caches until TTL: one `INFO keyspace` snapshot must prove
that every remaining DB-15 key has expiry (`keys == expires`), with no other
database or read/format error. Permanent or unknown residue fails CI. `DBSIZE`
is recorded before INFO; natural expiry may lower that count, while an increase
is rejected. Expiring residue is explicitly reported, never called exact
per-key cleanup or silently flushed. No document/key value is read. Actual
cleanup means the separate acknowledged removal of the two owned containers.

`config/config.yaml` must be absent in the public checkout. After upload-safety,
CI copies `server/config/config.example.yaml` to the disposable runner path.
No local runtime YAML or external credential is accepted. SMTP and legacy load
examples remain disabled. No MySQL, MinIO provider, RTC/LiveKit, native, UI89 or
other unprovisioned integration coverage is claimed.

## Reproducible images and evidence

The official `library/mongo:8.0.15` and `library/redis:8.2.2` **linux/amd64
manifest digests**, their parent index digests and the official metadata URLs
are recorded in `backend-integration.json`. These exact patch versions are CI
fixtures, not production-upgrade recommendations. Metadata was read without
pulling or starting local images:

- [Official Mongo tag metadata](https://hub.docker.com/v2/repositories/library/mongo/tags/8.0.15)
- [Official Redis tag metadata](https://hub.docker.com/v2/repositories/library/redis/tags/8.2.2)

Preparation checks pulled RepoDigests and actual Mongo/Redis versions. The ready
receipt binds the commit, run/attempt and all three script/contract hashes.

Always-run steps preserve per-package JSON events, stderr, vet, actual counts,
mandatory-test status, complete skip lists, image/startup metadata, container
logs, residual counts and exact-container cleanup results under
`ci-results/backend-integration/`. Console diagnostics are bounded, redacted and
prefixed to prevent test output from issuing workflow commands. Artifacts hold
only synthetic-run evidence; they do not contain supplied real credentials.

Finally, only the two containers whose labels match this run are removed,
including anonymous container volumes. No global Docker prune, Redis FLUSH,
Mongo repair/drop sweep, external service control or automatic test retry is
used. Functional results, residual checks and container disposal are separate
receipts; a missing stage never means the integration passed.
