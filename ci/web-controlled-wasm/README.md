# Controlled Web WASM continuation (8 tops / 20 subs)

This manual lane runs only the previously failed wasmwrapper (6 tops/3 subs)
and not-yet-started ownread (2 tops/17 subs), with exact run/pass/footer audit.
It fetches the fixed source, verifies tools, acquires and verifies the locked
modules, and checks source/modes before and after. It does not rebuild a product
WASM, run host bufferplan again, or replay the standalone ABI probe.

`reused-r2-validation.json` binds R2 run39004401325/head0fcf06a, source manifest
`d897d794...`, actual WASM/shim hashes, the successful build and host bufferplan
3 tops/4 subs (race), and Root's separate exact ABI145 startup observation.
That proves registration only: init/login/database/network all remain zero.
No R2 product artifact is downloaded into this CI. Executed8/20 and reused
3/4 plus ABI evidence remain distinct; this is not a newly executed11/24 suite.

Only js/wasm test execution sets GOMAXPROCS=1. Other host phase limits remain
unchanged. All source319/overlay19/protocol52/SDK review snapshots and locks
are unchanged. SDK/consumer snapshots stay under `ci/.../source`, not production.

Cleanup restores owner-write only on checked, canonical, this-UID directories
inside the task's own `cache/mod`, through stable no-follow descriptors. It
then removes that cache and the original exact owned trees. It never chmods
source trees, source files or external/shared caches.

Cold admission stays4GiB; continuous floor1GiB/.2s; owned8GiB; original phase
timeouts and outer2700s remain. Actual HOME is retained, Go locks are readonly,
only public fetch/acquisition is online, and execution remains in a real
nonroot empty network namespace. Output artifacts are continuation receipts,
not a new WASM. No SDK init, login, sends, deployment or full regression.

Root supplies the exact final manifest SHA:

```sh
python3 -B ci/web-controlled-wasm/run.py --manifest ci/web-controlled-wasm/final-source.json --manifest-sha256 FINAL_SHA --verify
```

New runtime/cache controls2 and directly affected old phase-order control1
are prepared for Root's one local targeted run. Original other3 controls and
prior successful Go/build/ABI results are reused, not repeated.
