"""Static artifact proof; deliberately no device, SDK-login or media operations."""
import hashlib
import json
from pathlib import Path
import re
import stat
import struct
import zipfile

from support import put, sha, write_json


def checked_zip(path):
    z = zipfile.ZipFile(path)
    entries = z.infolist()
    names = [e.filename for e in entries]
    if (len(entries) > 20000 or len(names) != len(set(names))
            or sum(e.file_size for e in entries) > 1024 ** 3):
        z.close()
        raise ValueError('bounded unique artifact ZIP required')
    for e in entries:
        if (e.flag_bits & 1 or stat.S_ISLNK(e.external_attr >> 16)
                or e.filename.startswith('/') or '\\' in e.filename
                or any(p in ('', '.', '..') for p in e.filename.rstrip('/').split('/'))
                or any(ord(c) < 32 for c in e.filename)):
            z.close()
            raise ValueError('unsafe artifact ZIP entry')
    if z.testzip() is not None:
        z.close()
        raise ValueError('artifact ZIP CRC mismatch')
    return z


def elf(data):
    if len(data) < 64 or data[:7] != b'\x7fELF\x02\x01\x01':
        raise ValueError('ELF64 little-endian input required')
    fields = struct.unpack_from('<HHIQQQIHHHHHH', data, 16)
    machine, offset, stride, count = fields[1], fields[4], fields[8], fields[9]
    if machine != 183 or stride != 56 or count == 0 or count > 1024 or offset + stride * count > len(data):
        raise ValueError('bounded AArch64 ELF program headers required')
    loads, relro = [], []
    for i in range(count):
        t, flags, off, addr, physical, size, memory, align = struct.unpack_from('<IIQQQQQQ', data, offset + stride * i)
        if off + size > len(data):
            raise ValueError('ELF segment outside file')
        if t == 1:
            loads.append({'align': align, 'offset': off, 'virtual_address': addr,
                          'congruent16KiB': (off - addr) % 16384 == 0})
        elif t == 0x6474e552:
            relro.append((addr + memory) % 16384)
    if not loads:
        raise ValueError('ELF has no LOAD segment')
    return {'machine': 'AArch64', 'loads': loads, 'RELRO_end_mod16384': relro,
            'LOAD_16KiB': all(x['align'] >= 16384 and x['congruent16KiB'] for x in loads),
            'RELRO_16KiB': bool(relro) and all(x == 0 for x in relro)}


def signatures(text):
    out = {}
    for m in re.finditer(r'public\s+static\s+native\s+(\S+)\s+(\w+)\s*\(([^)]*)\)\s*(?:throws\s+[^;]+)?;', text):
        types = tuple(' '.join(p.strip().split()[:-1]) for p in m[3].split(',') if p.strip())
        if m[2] in out:
            raise ValueError('ambiguous native binding overload')
        out[m[2]] = (m[1], types)
    return out


def bindings(source_jar, contract):
    with checked_zip(source_jar) as z:
        actual = signatures(z.read('open_im_sdk/Open_im_sdk.java').decode())
        for expected in contract['methods']:
            old = signatures(expected['historical_generated_Java_signature'])
            if actual.get(expected['method']) != old.get(expected['method']) or not old:
                raise ValueError('actual generated binding signature mismatch: ' + expected['method'])
        checked_callbacks = set()
        for site in contract['callback_implementation_sites']:
            interface = site['interface']
            if interface in checked_callbacks:
                continue
            text = z.read('open_im_sdk_callback/' + interface + '.java').decode()
            for method in site['historical_methods']:
                expected = re.sub(r'\s+', '', method['historical_signature'])
                if expected not in re.sub(r'\s+', '', text):
                    raise ValueError('generated callback contract differs: ' + interface)
            checked_callbacks.add(interface)
    return {'active_methods': len(contract['methods']), 'new_static_native_exports': len(actual),
            'callback_interfaces_checked': sorted(checked_callbacks),
            'new_generated_sources_sha256': sha(source_jar), 'new_generated_API_matched': True,
            'wrapper_compilation': 'still requires positive real Gradle build'}


def core(aar, metadata, ca_sha):
    if (metadata.get('artifact_sha256') != sha(aar) or metadata.get('only_debug_QA') is not True
            or metadata.get('production_replacement') is not False
            or metadata.get('qa_ca_sha256') != ca_sha
            or metadata.get('matches_original_abi_coverage') is not False
            or metadata.get('android_targets') != ['android/arm64']
            or metadata.get('base_commit') != '7b156fc5557878ad6e6a07e572694af5699c6624'
            or metadata.get('patch_sha256') != 'dcbc80f89ce07eaac8a2c28c23e1cbaef0db9c2c79cacffdaa020d61a356e1f7'
            or metadata.get('qa_overlay_sha256') != '72bb5adddedf262be493afa08bd4091a5dafc3a5596799b5a2fa7f272c37e0b3'
            or metadata.get('build_tags') != ['-tags=luobo_fixture_ca_debug']
            or metadata.get('go') != '1.27.2'
            or metadata.get('mobile') != 'v0.0.0-20261011034528-197770444867'):
        raise ValueError('fresh ARM64 QA provenance mismatch')
    with checked_zip(aar) as z:
        names = [n for n in z.namelist() if n.startswith('jni/') and n.endswith('.so')]
        if names != ['jni/arm64-v8a/libgojni.so'] or 'classes.jar' not in z.namelist():
            raise ValueError('exact ARM64 core ABI/classes required')
        body = z.read(names[0])
        proof = elf(body)
        if not proof['LOAD_16KiB'] or not proof['RELRO_16KiB']:
            raise ValueError('new core static16KiB check failed')
        if metadata['libraries'] != [{'abi': 'arm64-v8a', 'bytes': len(body),
                'sha256': hashlib.sha256(body).hexdigest()}]:
            raise ValueError('AAR library receipt mismatch')
    return {'AAR_sha256': sha(aar), 'ZIP_CRC': True, 'ABIs': ['arm64-v8a'], 'core_ELF': proof}


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
    if (not re.search(r"^package: name='com\.luoboduihua\.imapp'", badging, re.M)
            or 'application-debuggable' not in badging
            or "sdkVersion:'24'" not in badging or "targetSdkVersion:'36'" not in badging):
        raise ValueError('debug package/SDK identity differs')
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
