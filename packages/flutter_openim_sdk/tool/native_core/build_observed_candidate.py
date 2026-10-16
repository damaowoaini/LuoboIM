#!/usr/bin/env python3
"""Build the isolated compatibility candidate from pinned Apache-2.0 source.

This never writes the production plugin's android/libs or iOS Frameworks.
Supply the local audited protocol directory and an empty task-only work folder.
The manifest pins source/patches; artifact hashes are recorded after each build.
"""
import argparse
import base64
import hashlib
import io
import json
import os
import re
import stat
from pathlib import Path
import subprocess
import tarfile
import zipfile

HERE = Path(__file__).resolve().parent
PLUGIN_ROOT = HERE.parents[1]
BASE = "7b156fc5557878ad6e6a07e572694af5699c6624"
ANDROID_ABIS = {
    "android/arm": "armeabi-v7a",
    "android/arm64": "arm64-v8a",
    "android/386": "x86",
    "android/amd64": "x86_64",
}

def install_acquired_go_sum(work, reviewed, digest, source_digest):
    """Install only an exact, additive reviewed lock before any compilation."""
    work, reviewed = Path(work), Path(reviewed)
    if (work.absolute().resolve() != work.absolute() or not work.is_dir()
            or work.stat().st_uid != os.getuid()):
        raise ValueError('canonical owned candidate work required')
    target = work / 'go.sum'
    def read(path):
        if path.absolute().resolve() != path.absolute():
            raise ValueError('canonical non-symlink dependency lock required')
        with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
            info = os.fstat(f.fileno())
            if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                    or info.st_uid != os.getuid() or info.st_size > 262144):
                raise ValueError('bounded owned regular single-link dependency lock required')
            body = f.read(262145)
        if len(body) > 262144:
            raise ValueError('dependency lock exceeds bound')
        return body, info
    def sums(body):
        out = {}
        for line in body.decode('utf-8').splitlines():
            fields = line.split()
            if len(fields) != 3 or not fields[2].startswith('h1:'):
                raise ValueError('invalid dependency checksum line')
            key = tuple(fields[:2])
            if key in out or len(base64.b64decode(fields[2][3:], validate=True)) != 32:
                raise ValueError('duplicate or invalid dependency checksum')
            out[key] = fields[2]
        return out
    def write(path, body, mode=0o600):
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
        with os.fdopen(fd, 'wb') as f:
            f.write(body); f.flush(); os.fsync(f.fileno())
    def sync():
        fd = os.open(work, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
    before, original = read(target)
    after, _ = read(reviewed)
    if (not re.fullmatch('[0-9a-f]{64}', digest)
            or hashlib.sha256(before).hexdigest() != source_digest
            or hashlib.sha256(after).hexdigest() != digest):
        raise ValueError('source or acquired dependency lock SHA mismatch')
    old, new = sums(before), sums(after)
    if not old or any(new.get(k) != value for k, value in old.items()):
        raise ValueError('acquired lock removed or changed a source checksum')
    proof = {'source_go_sum_sha256': source_digest, 'acquired_go_sum_sha256': digest,
             'source_bytes': len(before), 'acquired_bytes': len(after),
             'added_checksums': len(new) - len(old), 'old_checksums_preserved': True}
    write(work / 'source-go.sum', before)
    write(work / 'acquired-dependency-lock.json', (json.dumps(proof, indent=2) + '\n').encode())
    temporary = work / '.acquired-go.sum.tmp'
    write(temporary, after, stat.S_IMODE(original.st_mode))
    sync()
    current, info = read(target)
    if (current != before or (info.st_dev, info.st_ino, info.st_mtime_ns, info.st_ctime_ns)
            != (original.st_dev, original.st_ino, original.st_mtime_ns, original.st_ctime_ns)):
        raise ValueError('source lock changed before acquired lock installation')
    os.replace(temporary, target); sync()
    if read(target)[0] != after:
        raise ValueError('acquired dependency lock installation failed verification')
    return proof


def run(argv, cwd=None, env=None):
    return subprocess.run(list(map(str, argv)), cwd=cwd, env=env, check=True)


def verify_protocol(protocol, entries):
    if not (protocol / "go.mod").is_file():
        raise SystemExit("audited server protocol module is missing")
    expected = {entry["path"] for entry in entries}
    if len(expected) != len(entries):
        raise SystemExit("duplicate reviewed protocol input")
    for entry in entries:
        path = protocol / entry["path"]
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != entry["sha256"]:
            raise SystemExit("protocol differs from reviewed manifest: " + entry["path"])
    # go/build/build.go fileListForExt also admits native objects and SWIG /
    # Cgo inputs. They must not bypass the reviewed protocol input inventory.
    source_suffixes = {
        ".go", ".proto", ".c", ".cc", ".cpp", ".cxx", ".m", ".mm",
        ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90",
        ".s", ".S", ".sx", ".swig", ".swigcxx", ".syso",
    }
    actual = {
        str(path.relative_to(protocol)) for path in protocol.rglob("*")
        if path.is_file() and (path.suffix in source_suffixes or path.name in {"go.mod", "go.sum"})
    }
    if actual != expected:
        raise SystemExit("protocol source inventory differs from the reviewed manifest")

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", required=True, type=Path)
    parser.add_argument("--protocol", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--go", type=Path, default=Path("go"))
    parser.add_argument("--mobile-tools", type=Path)
    parser.add_argument("--android-sdk", type=Path)
    parser.add_argument("--ndk", type=Path)
    parser.add_argument("--java-home", type=Path)
    parser.add_argument(
        "--qa-fixture-ca", type=Path,
        help="Explicit isolated debug QA only: append this public pinned CA for the exact local API/WSS fixture.",
    )
    parser.add_argument("--qa-fixture-ca-sha256")
    parser.add_argument(
        "--android-targets", nargs="+", choices=ANDROID_ABIS,
        default=["android/arm64"],
        help="Explicit research ABI set; preserving production coverage requires all four targets.",
    )
    parser.add_argument("--prepare-only", action="store_true")
    parser.add_argument("--acquired-go-sum", type=Path)
    parser.add_argument("--acquired-go-sum-sha256")
    parser.add_argument("--go-observer", type=Path)
    parser.add_argument("--go-observer-sha256")
    parser.add_argument("--observer-config", type=Path)
    parser.add_argument("--observer-config-sha256")
    args = parser.parse_args()
    work = args.work.resolve()
    if work.is_relative_to(PLUGIN_ROOT):
        parser.error("candidate work must stay outside the production SDK package")
    if args.output is not None:
        output = args.output.resolve()
        if output.is_relative_to(PLUGIN_ROOT):
            parser.error("candidate output must never overwrite the production SDK package")
        if output.suffix != ".aar":
            parser.error("candidate Android output must have an .aar extension")
        if output.exists() or output.with_suffix(".build.json").exists():
            parser.error("candidate output and build manifest must be new paths; existing evidence is never overwritten")
    if len(args.android_targets) != len(set(args.android_targets)):
        parser.error("Android targets must be unique")
    qa = args.qa_fixture_ca is not None or args.qa_fixture_ca_sha256 is not None
    acquired = args.acquired_go_sum is not None or args.acquired_go_sum_sha256 is not None
    observing = [args.go_observer, args.go_observer_sha256, args.observer_config, args.observer_config_sha256]
    if (not args.prepare_only and (not qa or not acquired or not all(observing)
                                  or args.android_targets != ['android/arm64'])):
        parser.error('observed builder requires explicit acquired debug QA ARM64 and all observer pins')
    if args.prepare_only and any(observing):
        parser.error('prepare-only never installs a bind observer')
    if acquired and (not qa or args.prepare_only or args.acquired_go_sum is None or args.acquired_go_sum_sha256 is None):
        parser.error("acquired lock requires both flags and explicit debug-QA build, never prepare-only")
    if acquired and args.acquired_go_sum.absolute() != HERE / "acquired-go.sum":
        parser.error("acquired lock must be the reviewed input beside this builder")
    if qa and (args.qa_fixture_ca is None or args.qa_fixture_ca_sha256 is None):
        parser.error("QA trust requires both --qa-fixture-ca and --qa-fixture-ca-sha256")
    qa_certificate = None
    if qa:
        if not re.fullmatch(r"[0-9a-f]{64}", args.qa_fixture_ca_sha256):
            parser.error("QA CA SHA256 must be lowercase hexadecimal")
        with args.qa_fixture_ca.open("rb") as certificate_file:
            qa_certificate = certificate_file.read(32769)
        if len(qa_certificate) > 32768 or not re.fullmatch(
            rb"\s*-----BEGIN CERTIFICATE-----\s+[A-Za-z0-9+/=\r\n]+-----END CERTIFICATE-----\s*",
            qa_certificate,
        ):
            parser.error("QA CA must contain exactly one public PEM certificate")
        if hashlib.sha256(qa_certificate).hexdigest() != args.qa_fixture_ca_sha256:
            parser.error("QA CA checksum mismatch")
    manifest = json.loads((HERE / "candidate-source-manifest.json").read_text())
    patch = HERE / manifest["patch"]
    if hashlib.sha256(patch.read_bytes()).hexdigest() != manifest["patch_sha256"]:
        raise SystemExit("candidate patch checksum mismatch")
    commit = subprocess.check_output(["git", "-C", str(args.upstream), "rev-parse", BASE + "^{commit}"], text=True).strip()
    if commit != BASE:
        raise SystemExit("unexpected upstream revision")
    verify_protocol(args.protocol, manifest["protocol"]["files"])
    work = args.work.resolve()
    if work.exists() and any(work.iterdir()):
        raise SystemExit("work directory must be empty; existing files are never overwritten")
    work.mkdir(parents=True, exist_ok=True)
    archive = subprocess.check_output(["git", "-C", str(args.upstream), "archive", BASE])
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        for member in bundle.getmembers():
            target = (work / member.name).resolve()
            if not target.is_relative_to(work) or member.issym() or member.islnk():
                raise SystemExit("unsafe upstream archive member")
        bundle.extractall(work)
    run(["git", "apply", "--check", patch], cwd=work)
    run(["git", "apply", patch], cwd=work)
    for entry in manifest["modified_files"]:
        actual = hashlib.sha256((work / entry["path"]).read_bytes()).hexdigest()
        if actual != entry["sha256"]:
            raise SystemExit("prepared source differs from reviewed manifest: " + entry["path"])
    qa_manifest = None
    if qa:
        qa_manifest = json.loads((HERE / "qa-fixture-source-manifest.json").read_text())
        if qa_manifest["requires_base_patch_sha256"] != manifest["patch_sha256"]:
            raise SystemExit("QA overlay does not match reviewed base patch")
        qa_patch = HERE / qa_manifest["overlay"]
        if hashlib.sha256(qa_patch.read_bytes()).hexdigest() != qa_manifest["overlay_sha256"]:
            raise SystemExit("QA overlay checksum mismatch")
        run(["git", "apply", "--check", qa_patch], cwd=work)
        run(["git", "apply", qa_patch], cwd=work)
        for entry in qa_manifest["modified_files"]:
            if hashlib.sha256((work / entry["path"]).read_bytes()).hexdigest() != entry["sha256"]:
                raise SystemExit("QA prepared source differs from reviewed manifest: " + entry["path"])
        generated = work / qa_manifest["generated_file"]
        generated.write_text(
            "// Generated explicit QA certificate; do not publish this artifact.\n"
            "//go:build luobo_fixture_ca_debug\n\npackage fixturetls\n\n"
            + "const fixtureCAPEM = " + json.dumps(qa_certificate.decode("ascii")) + "\n"
            + "const fixtureCASHA256 = " + json.dumps(args.qa_fixture_ca_sha256) + "\n"
        )
        (work / "qa-source.json").write_text(json.dumps({
            "overlay_sha256": qa_manifest["overlay_sha256"],
            "ca_sha256": args.qa_fixture_ca_sha256,
            "generated_source_sha256": hashlib.sha256(generated.read_bytes()).hexdigest(),
            "build_tag": qa_manifest["build_tag"],
            "only_debug_QA": True,
            "production_replacement": False,
        }, indent=2) + "\n")
    version = subprocess.check_output([str(args.go), "version"], text=True)
    if "go1.27.2 " not in version:
        raise SystemExit("this candidate was validated with Go 1.27.2")
    run([args.go, "mod", "edit", "-replace=github.com/OpenIMSDK/protocol=" + str(args.protocol.resolve())], cwd=work)
    if args.prepare_only:
        print("CANDIDATE_SOURCE_PREPARED")
        return
    if any(value is None for value in [args.output, args.mobile_tools, args.android_sdk, args.ndk, args.java_home]):
        raise SystemExit("Android build requires output/mobile-tools/android-sdk/ndk/java-home")
    acquired_proof = None
    if acquired:
        source_sum = next(x['sha256'] for x in manifest['modified_files'] if x['path'] == 'go.sum')
        acquired_proof = install_acquired_go_sum(work, args.acquired_go_sum, args.acquired_go_sum_sha256, source_sum)
    module_before = (work / 'go.mod').read_bytes()
    def check_locks():
        if acquired and ((work / 'go.mod').read_bytes() != module_before
                         or hashlib.sha256((work / 'go.sum').read_bytes()).hexdigest() != args.acquired_go_sum_sha256):
            raise SystemExit('readonly build changed reviewed dependency locks')
    environment = dict(os.environ)
    environment.update({"ANDROID_HOME": str(args.android_sdk.resolve()), "ANDROID_NDK_HOME": str(args.ndk.resolve()), "JAVA_HOME": str(args.java_home.resolve()), "CGO_LDFLAGS": "-Wl,-z,max-page-size=16384 -Wl,-z,common-page-size=16384"})
    environment["PATH"] = str(args.go.resolve().parent) + os.pathsep + str(args.mobile_tools.resolve()) + os.pathsep + environment.get("PATH", "")
    if acquired:
        flags = environment.get('GOFLAGS', '').split()
        if any(flag.startswith('-mod=') and flag != '-mod=readonly' for flag in flags):
            raise SystemExit('conflicting Go module mode in acquired QA build')
        environment['GOFLAGS'] = ' '.join(flags + ([] if '-mod=readonly' in flags else ['-mod=readonly']))
        environment['GOWORK'] = 'off'
    check_locks()
    if qa:
        # Validate the actually embedded public CA, both TLS protocol controls
        # and the default-build trust stub before starting a native build.
        run([args.go, "test", "./pkg/fixturetls", "-count=1"], cwd=work, env=environment if acquired else None)
        check_locks()
        run([args.go, "test", "-tags=" + qa_manifest["build_tag"], "./pkg/fixturetls", "-count=1"], cwd=work, env=environment if acquired else None)
        check_locks()
    output = args.output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    # Original builder remains unchanged. This separately named entry adds
    # evidence only around the actual offline bind; no generated lock install.
    for path, expected, maximum in [(args.go_observer, args.go_observer_sha256, 131072),
                                    (args.observer_config, args.observer_config_sha256, 16384)]:
        if (path.resolve() != path.absolute() or path.stat().st_uid != os.getuid()
                or path.is_symlink() or not path.is_file() or path.stat().st_nlink != 1
                or path.stat().st_size > maximum
                or hashlib.sha256(path.read_bytes()).hexdigest() != expected):
            raise SystemExit('owned exact bounded observer input required')
    if (args.go_observer.absolute() != work.parent / 'observer/go'
            or args.observer_config.absolute() != work.parent / 'observer/config.json'):
        raise SystemExit('observer must belong to this build job')
    config = json.loads(args.observer_config.read_text())
    if config['base_path'] != environment['PATH'] or config['real_go'] != str(args.go.resolve()):
        raise SystemExit('observer original PATH/real Go mismatch')
    environment['PATH'] = str(args.go_observer.parent) + os.pathsep + environment['PATH']
    environment['LUOBO_GO_OBSERVER_CONFIG'] = str(args.observer_config)
    environment['LUOBO_GO_OBSERVER_CONFIG_SHA256'] = args.observer_config_sha256
    build_tags = ["-tags=" + qa_manifest["build_tag"]] if qa else []
    run([(args.mobile_tools / "gomobile").resolve(), "bind", "-work", *build_tags, "-target=" + ",".join(args.android_targets), "-androidapi=24", "-trimpath", "-ldflags=-s -w", "-o", output, "./open_im_sdk", "./open_im_sdk_callback"], cwd=work, env=environment)
    check_locks()
    libraries = []
    with zipfile.ZipFile(output) as aar:
        for name in sorted(aar.namelist()):
            if name.startswith("jni/") and name.endswith("/libgojni.so"):
                content = aar.read(name)
                libraries.append({"abi": name.split("/")[1], "bytes": len(content), "sha256": hashlib.sha256(content).hexdigest()})
    expected = {ANDROID_ABIS[target] for target in args.android_targets}
    if {entry["abi"] for entry in libraries} != expected or len(libraries) != len(expected):
        raise SystemExit("generated AAR ABI set differs from requested targets")
    output.with_suffix(".build.json").write_text(json.dumps({"bind_observer": {"source_sha256": args.go_observer_sha256, "config_sha256": args.observer_config_sha256, "retain_actual_work": True, "metadata_lock_installed": False}, "acquired_dependency_lock": acquired_proof, "readonly_dependency_mode": acquired, "dependency_go_mod_sha256": hashlib.sha256(module_before).hexdigest(), "base_commit": BASE, "patch_sha256": manifest["patch_sha256"], "qa_overlay_sha256": qa_manifest["overlay_sha256"] if qa else None, "qa_ca_sha256": args.qa_fixture_ca_sha256 if qa else None, "build_tags": build_tags, "only_debug_QA": qa, "artifact_sha256": hashlib.sha256(output.read_bytes()).hexdigest(), "artifact_bytes": output.stat().st_size, "android_targets": args.android_targets, "libraries": libraries, "matches_original_abi_coverage": expected == set(ANDROID_ABIS.values()), "go": "1.27.2", "mobile": "v0.0.0-20261011034528-197770444867", "android_api": 24, "production_replacement": False, "runtime_validation": "pending for this generated artifact; ABI inventory and ELF checks are not runtime proof"}, indent=2) + "\n")
    print("ISOLATED_CANDIDATE_AAR_BUILT")

if __name__ == "__main__":
    main()
