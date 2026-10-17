# Controlled Web WASM targeted build

This manual lane produces an independent review artifact. It does not copy a
WASM/shim to public, initialize an SDK, log in, open a business database, send a
message, invoke native bind, or run the whole existing suites.

The fixed base is Git commit `7b156fc5557878ad6e6a07e572694af5699c6624` from the
public OpenIM SDK core origin, then the exact enterprise compatibility patch
`dcbc80f89ce07eaac8a2c28c23e1cbaef0db9c2c79cacffdaa020d61a356e1f7`.
`base-inputs.json` binds all 309 prepared core bodies/modes and 52 protocol
bodies/modes. The reviewed additive acquired-go.sum preserves every old checksum.
The absolute local protocol replace is changed exactly once to `../protocol`
inside the task's private source; before and after go.mod hashes are recorded.
No QA TLS source tree is copied.

Root must first seal and commit the combined compat21 + real buffer + own-read
source and support manifest. The workflow takes its canonical repository path
and SHA256 explicitly. The controller will not guess a final SHA or run from an
unsealed partial144 API source. The final manifest requires these fields:

- `schemaVersion: 1`, `baseCommit`, `baseInputsSHA256`, `harnessInputsSHA256`.
- `goSumSHA256` equal to the acquired reviewed lock, `requiredABI: 145`.
- `core`: complete sorted final rows `{path,bytes,sha256,mode}` and `coreDigest`:
  ascending UTF8 path + NUL + ASCII SHA256 + LF, then SHA256.
- `overlay`: every unique changed/new Go row
  `{path,target,beforeSHA256,bytes,sha256,mode}`. `path` lies under
  `ci/web-controlled-wasm/source/core/`; `target` is relative to the core.
  `beforeSHA256` is exact fixed base body or null for a new file.
- `support`: exact committed source rows for the composed SDK patch files,
  author factories, source provenance and other integration locks. These are
  checked before/after and not executed by this build lane.

Invocation after Root adoption:

```sh
python3 -B ci/web-controlled-wasm/run.py --manifest ci/web-controlled-wasm/final-source.json --manifest-sha256 FINAL_SHA --verify
# The workflow alone invokes the guarded execution after official pinned setup.
```

There are three targeted Go commands: native race bufferplan 3 tops/4 subs;
js/wasm wrapper 6 tops/3 subs; js/wasm own-read core 2 tops/17 subs (11 tops/24 subs total). All compile
with `-mod=readonly -p=1`; only explicit top names run, exact child names and one
package footer are audited. The js/wasm tests use the same Go1.27.2
`go_js_wasm_exec` and Node24.21.0. Go js/wasm does not support race, so those two
commands are not claimed to be race tested. No SQL/Mongo/browser fixture runs.

Only public Git fetch and reviewed module acquisition have networking.
Dependency environment uses fixed `https://proxy.golang.org`, `sum.golang.org`,
`GOAUTH=off`, empty private/proxy overrides and no ambient credentials. Every
other phase verifies a real non-root kernel network namespace. After download,
`GOPROXY=off/GOSUMDB=off`, toolchain local and source/locks read-only by contract.
Tests may write only task caches/temp; any changed source/mode/lock rejects.
The source-only metadata verify does not materialize or execute Go.

Cold admission is 4GiB free. Continuous resource guards use 1GiB free, .2s poll,
8GiB owned allocation, per-phase timeouts plus a 2700s outer bound. All commands
are joined by the existing resource guard. Only the task's fresh cache, temp,
Git download and materialized source directories are removed; raw evidence,
ownership record and independent products remain. No shared cache is read or
removed. Actual HOME is preserved; all Go caches/TMP paths are task-owned.

The rebuilt artifact is compiled from this explicit source. It is not asserted
byte-equivalent to the opaque old WASM. ABI checks require the exact observed145
function set, not merely a count; registration is not proof of every API's
business behavior. No startup probe calls `initSDK`, `login`, a database or the
network. The actual worker SQL/IndexedDB browser fixture and Web event consumer
are separate Root-owned validations; this lane cannot claim those passed.

Three new pure controller controls are prepared for a single local review run.
The cloud workflow does not repeat them. At preparation there are zero Python
control runs, Go tests, builds, downloads, SDK initializations or dispatches.
