# Apple SDK-only ARM64 — R7 actual bind / R8 link-only preparation

R7 run38955829570@3ec355b8 actually completed device+simulator Go bind (exit0).
Both actual generated tidy/list/module provenance passed the fixed632-row union;
core8eb9 locks stayed unchanged. Each platform retained42 unchanged ordinary
generated source files:43 before/44 after also record the expected go.mod change
and added go.sum. The first Xcode ar audit rejected the original fat wrapper;
only the target was single ARM64, not a byte-proven slice count. No actual SDK Swift,
ObjC shim, SDK dylib or probe link started; this is not a Swift compile failure.
Raw artifact12071020431 SHA271e8045baae5337f4c7efef55ea5bac28777e528264e966c948349e14a78bf2
is separate from products12070947280 SHA763d842282bc48dc04679e26443a8c8c004e698ced4be2e8e4ca8cabd0760459.

R8 preserves this full-bind lane, its original inputs/resources/network gates
and original framework link input. The audit first requires exact ARM64 and a
strict single-ARM64 static archive, then uses an owned thin copy for ar/file/nm/
Mach-O member audits. The independent `ci/apple-sdk-link` manual-only lane can
reuse only those exact R7 products and raw custody, without repeating Go bind.
Its six new pure Python controls passed once (0.015s; guard0.213s, continuous
1GiB/.2s,64MiB/30s). These mocked/synthetic controls do not verify product
binaries. Both real platform link gates remain prepared/unexecuted. No App, embedding, signing or device acceptance is claimed.

Both original framework plists declare MinimumOSVersion100.0. This is the fixed
gomobile static-framework/SPM workaround, independent of the actual min15 compiler
flags. R8 preserves and explicitly checks those exact original plist bytes; it
must also prove static core/member platform+MinOS and no dynamic OpenIMCore
plugin dependency. It does not turn this metadata workaround into App/iOS15
packaging acceptance, nor silently rewrite the original plist to15.

## Historical R6-R1 preparation and evidence boundary


The explicit cloud lane remains `apple-sdk-only-cloud-arm64-r1`, SDK-only device
and simulator arm64/min15, with no App/signing/device claim. R6 is an explicit
registry/source revision and rejects older work through the existing registry SHA.
Original local R1 nine files and every R2–R5 failure/evidence stay historical.

R5 really ran `go mod download all` in core. Its actual generated iOS and simulator
tidy-v failed on compute1.20.1 offline; no archive/Swift/ObjC link completed. Core
8eb9 was intact. The generated original97 template matches the independently
accepted Android projection after only the two owned replace paths are normalized.

R6 setup keeps core acquisition unchanged. A separate owned metadata-only warm
root starts from original97 + independent610-line sum, sharing only the job cache.
`download all` may perform only the reviewed8 byte-exact version changes; both
warm locks then remain fixed through offline verify and selected graph checks.
610 hashes cover359 identities:251 ZIP+mod pairs and108 mod-only. No per-module
ZIP fetch loop, empty-directory tidy, version forcing or bind-error retry exists.
The warm locks are never copied into actual generated bind trees.

Actual bind remains network-denied. Its original plain/verbose tidy argv is
forwarded unchanged; Go/ObjC/header/module before+after/failure snapshots remain.
On successful real tidy, one fixed real-Go `list -m -json all` read-only audit
checks generated requires, selected versions and only two owned replacements.
Every selected external .mod is read from the controller-bound owned GOMODCACHE,
using official Go cache escaping and single-go.mod dirhash.Hash1, and must match
its independent610 h1. Graph GoMod pointers are never read. GoModSum/Sum are
optional Go JSON fields; when present they must match the finite catalogue too. Unknowns fail even if an unrelated
tool-install cache contains them. Actual tidy may prune metadata rows; the610 lock
is provenance/cache input, not an asserted full-bind lock. This audit runs before
the observer returns to gomobile's archive compile. It shares signal forwarding,
stdio evidence and the outer exact monitored process group.

All prior environment/Git isolation, official Go1.27.2/mobile/engine inputs,
static-core/platform/minOS,18 Swift+ObjC and linked-binary gates remain. Resources
are unchanged: cold setup8GiB/allocated3GiB/1200s; build4GiB initial, continuous
1GiB/.2s, all-owned6GiB+build3GiB/1800s. New warm caches/locks/raw count in these
caps. No host HOME rewrite, shared cache deletion or extra network for build.

Prepared only: `python3.12 -B -m unittest -v test_generated_module.GeneratedClosure`
has4 new pure controls, including actual observer.main with a Python child. It
never executes Go/native/network and does not prove OS cancellation or SDK link.
Root must review then run only those4 under private TMP/network deny/1GiB/.2s/
64MiB/30s. Reuse unchanged parser16/cloud12/env1/DNS2/streams3/observer3 evidence;
no old/full suite rerun is requested. No local Go, downloads or cloud dispatch
occurred in this source preparation.

R6-R1 source correction: accepted R9 actual95-record post-tidy graph omitted
GoModSum in61 external records; this is not hash evidence. Its actual GoMod line3
was go1.27 and line8 the expected core v3 pseudo-version. Original R6 freeze stays
immutable. Four controls remain unrun, now including real cached compute1.20.1
mod bytes+h1, missing/corrupt/symlink/hardlink/wrong-case path rejection and a real
Python-child observer graph with omitted optional fields. No Go/cache download is
added; absent .mod remains a refusal. The original successful native run is not
claimed to have exercised R6-R1.
