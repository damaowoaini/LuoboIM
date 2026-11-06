"""Current363 badging-label adapter; shared audit/helpers stay immutable.

Only the SDK-label predicate and the per-consumer audit call differ from the
controlled originals. No logs are rewritten and no historical globals mutate.
"""
import json
from pathlib import Path
import re

import audit as historical_audit
from audit import checked_zip, elf, put, sha
import support as s


def validate_badging_identity(badging):
    if (not re.search(r"^package: name='com\.luoboduihua\.imapp'", badging, re.M)
            or 'application-debuggable' not in badging):
        raise ValueError('debug package/SDK identity differs')
    minimum = []
    targets = []
    for line in badging.splitlines():
        key = line.lstrip(' \t').split(':', 1)[0].strip().casefold()
        if key in ('sdkversion', 'minsdkversion'):
            match = re.fullmatch(r"(sdkVersion|minSdkVersion):'([0-9]+)'", line)
            if match is None:
                raise ValueError('debug package/SDK identity differs')
            minimum.append((match.group(1), match.group(2)))
        elif key == 'targetsdkversion':
            match = re.fullmatch(r"targetSdkVersion:'([0-9]+)'", line)
            if match is None:
                raise ValueError('debug package/SDK identity differs')
            targets.append(match.group(1))
    # Both aliases present, even with the same value, are duplicate declarations.
    if len(minimum) != 1 or minimum[0][1] != '24' or targets != ['36']:
        raise ValueError('debug package/SDK identity differs')
    return {'min_sdk_label': minimum[0][0], 'min_sdk': 24, 'target_sdk': 36}


def apk(path, work, run, env, tools, aar):
    work = Path(work)
    bt = work / 'android/build-tools/36.0.0'
    with checked_zip(path) as z:
        names = z.namelist()
        required = {'AndroidManifest.xml', 'classes.dex', 'lib/arm64-v8a/libflutter.so', 'lib/arm64-v8a/libgojni.so'}
        if not required.issubset(names) or 'res/raw/luobo_fixture_ca.pem' in names:
            raise ValueError('expected debug APK content or default-system Android trust differs')
        libs = {}
        for n in names:
            if n.startswith('lib/') and n.endswith('.so'):
                if not n.startswith('lib/arm64-v8a/'):
                    raise ValueError('unexpected APK ABI')
                libs[n] = elf(z.read(n))
        apk_core = z.read('lib/arm64-v8a/libgojni.so')
    raw = work / 'audit/core-strip/libgojni.so'
    with checked_zip(aar) as z:
        raw_core = z.read('jni/arm64-v8a/libgojni.so')
    put(raw, raw_core)
    strip = work / 'android/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-strip'
    run([strip, '--strip-unneeded', raw], work, env, 'audit-core-strip', 120)
    if apk_core not in (raw_core, raw.read_bytes()):
        raise ValueError('APK core is neither new AAR bytes nor exact AGP strip transform')
    badging = run([bt / 'aapt2', 'dump', 'badging', path], work, env, 'audit-badging', 120).read_text()
    validate_badging_identity(badging)
    resources = run([bt / 'aapt2', 'dump', 'resources', path], work, env, 'audit-resources', 120).read_text()
    if re.search(r'\b(?:raw|(?:string))/luobo_fixture_ca(?:_sha256)?\b', resources):
        raise ValueError('unrequested Android fixture trust resource packaged')
    run([bt / 'apksigner', 'verify', '--verbose', '--print-certs', path], work, env, 'audit-signature', 120)
    run([bt / 'zipalign', '-c', '-P', '16', '4', path], work, env, 'audit-zipalign16', 120)
    return {'APK_sha256': sha(path), 'APK_bytes': path.stat().st_size, 'ZIP_CRC': True,
            'ABIs': ['arm64-v8a'], 'libraries': libs,
            'all_LOAD_16KiB': all(v['LOAD_16KiB'] for v in libs.values()),
            'new_core_exact_raw_or_strip': True, 'package': 'com.luoboduihua.imapp',
            'debuggable': True, 'signature_verified': True, 'zipalign16_verified': True,
            'device_or_Android16KiB_runtime': False, 'production4ABI_parity': False}


def build_apk(self, parent):
    netns = parent.netns
    environment = parent.environment
    netns()
    w = self.w
    self.verify()
    self.check_java_lock()
    self.check_packages()
    e = environment(w, True)
    cwd = w / 'source/app/android'
    aar = w / 'artifacts/core-current257-arm64.aar'
    alias = w / 'negative/missing-provenance.aar'
    s.put(alias, aar.read_bytes())
    cases = [(task.lower(), self.gradle(task), 'QA core requires assembleDebug')
             for task in ['assembleRelease', 'assembleProfile', 'assemble']]
    cases += [('missing-sha', self.gradle('assembleDebug', digest=False), 'QA core requires an explicit AAR path and lowercase SHA256.'),
              ('wrong-sha', self.gradle('assembleDebug', digest='0' * 64), 'QA core AAR checksum mismatch.'),
              ('missing-manifest', self.gradle('assembleDebug', aar=alias, digest=s.sha(aar)), 'QA core requires its bounded, auditable build manifest.')]
    for label, args, reason in cases:
        self.check_java_lock()
        self.cmd(args, cwd, e, 'gate-' + label, 180, reason)
        self.verify()
    altered = json.loads(aar.with_suffix('.build.json').read_text())
    altered['qa_overlay_sha256'] = '0' * 64
    s.write_json(alias.with_suffix('.build.json'), altered)
    self.cmd(self.gradle('assembleDebug', aar=alias, digest=s.sha(aar)), cwd, e, 'gate-wrong-overlay', 180,
             'QA core build manifest does not match the reviewed debug trust artifact.')
    if list((w / 'source/app/build').rglob('*.apk')):
        raise ValueError('negative gate unexpectedly emitted APK')
    self.check_java_lock()
    log = self.cmd(self.gradle('assembleDebug'), cwd, e, 'assemble-debug', 1800)
    text = log.read_text(errors='replace')
    if not re.search(r'^BUILD SUCCESSFUL in .+$', text, re.M) or re.search(r'^BUILD FAILED', text, re.M):
        raise ValueError('complete positive Gradle success marker missing')
    for task in [':flutter_openim_sdk:compileDebugJavaWithJavac', ':app:compileDebugKotlin']:
        if not re.search(r'^> Task ' + re.escape(task) + r'\s*$', text, re.M):
            raise ValueError('actual uncached wrapper/native bridge compilation not observed: ' + task)
    outputs = list((w / 'source/app/build/app/outputs/apk/debug').glob('*.apk'))
    if len(outputs) != 1:
        raise ValueError('exactly one arm64 debug APK required')
    final = w / 'artifacts/luobo-current257-auditable-arm64-debug.apk'
    s.put(final, outputs[0].read_bytes())
    def execute(args, directory, environment_, label, timeout):
        return self.cmd(args, directory, environment_, label, timeout)
    proof = apk(final, w, execute, e, self.tools, aar)
    proof.update({'negative_gates': 7, 'all29_actual_wrapper_sources_compiled_by_real_app': True,
                  'source_members': 427, 'source_digest': self.inputs['source_digest'], 'entry': 'lib/main.dart',
                  'business_defines_or_old_CA_or_device_operations': False})
    s.write_json(w / 'evidence/APK-audit.json', proof)
    self.verify()
    self.check_java_lock()
    self.check_packages()
    if (w / 'job-private/job-ca.key').exists() or any(p.is_symlink() for p in (w / 'artifacts').rglob('*')):
        raise ValueError('private key or artifact symlink must not be published')
