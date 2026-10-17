# Controlled Web ownread continuation (2 tops / 17 subs)

This manual lane runs only ownread, whose R3 package failed compilation before
any selected case started. R3 wasmwrapper6/3 already passed. R2 product build,
host bufferplan3/4 (race), and Root ABI145 registration are retained as separate
source-bound evidence and are not replayed. No new product WASM is built or
downloaded into this CI. The exact targeted result is only2/17.

The shared legacy test helper now returns db_interface.DataBase. All its old
test bodies/HTTP fixture/assertions remain exact. The complete319-source vector
has a new digest and20 overlays. The reuse gate verifies both source rows,
reverses only the exact import/type edits, and reconstructs the old full319
digest. All production source/SDK support/protocol52 and locks remain identical;
the fixed production tree has no go:embed directives that could include the
changed _test.go. Old and new core digests are never aliases.

`reused-r2-validation.json` and `test-only-reused-r3-validation.json` bind prior
run/head/source/artifact hashes, actual passed counts, sourcepost and cleanup.
They do not claim full API semantics, SDK initialization or current business
validation. Only current ownread uses GOMAXPROCS1. Host phases retain their
original bounds and fresh owned cache. R3's checked module-cache cleanup is
unchanged; no source/shared/external mode is modified.

Cold4GiB/floor1GiB/.2s/owned8GiB and original timeouts remain. Source/locks are
readonly by contract; only fixed public acquisition is online, execution uses
a real nonroot empty network namespace, and HOME remains actual. Artifacts
contain continuation receipts, no new WASM. Nothing is deployed.

Root provides the final manifest SHA to `run.py --manifest
ci/web-controlled-wasm/final-source.json --manifest-sha256 SHA --verify`.
Only directly affected runtime/phase-order controls and the new test-only
reuse control are planned locally. Other valid controls are reused.
