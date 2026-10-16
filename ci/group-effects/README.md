# Isolated typed group effects

This workflow validates 15 new source files against the accepted 12-file group
authority foundation and the exact 927-file public server snapshot. It runs all
27 prepared top-level tests and 18 subtests in two packages, with real temporary
Mongo transactions, dropped replies, and owned HTTP callbacks. A skipped Mongo
test is a failure. Both packages require race tests and vet; dependency locks
and every materialized source byte must remain unchanged.

The candidate stays under `ci/candidates/group-effects/source/`. The workflow
does not replace server packages, wire group RPCs, dispatch real notifications,
deploy a worker, or change a running service. Callback acknowledgement follows
the existing OpenIM application response contract; it is not independent proof
of a remote transaction. Unknown outcomes remain durable and inspectable.

Only a fresh GitHub-hosted Linux runner may execute Go or create fixtures.
Existing exact run-labelled Mongo/Redis setup, residual checks and cleanup are
reused. Preparation and Go commands retain resource logs with a 1 GiB free-space
floor and 0.2 s sampling; source, module cache and build cache are owned by the
job, with a 16 GiB allocation cap. The source copy requires 4 GiB free initially.
No local Go, database, native build, or package download is part of adoption.

The command capture keeps actual JSON stdout separate from dependency download
diagnostics on stderr. Both original streams and the guard receipt are retained;
the strict completion parser still rejects malformed JSON or missing test cases.
