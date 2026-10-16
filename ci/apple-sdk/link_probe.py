"""Compile every real SDK Swift file, real ObjC shim, then link both Apple slices."""
import json
from pathlib import Path
import plistlib
import re

from apple_core import command, digest, inputs, put, put_dir, read, record
from core_binary import audit_core, macho, plugin_dependencies, MH_DYLIB, MH_EXECUTE


def target(kind):
    if kind not in {'device', 'simulator'}:
        raise ValueError('only real Apple device/simulator target')
    return 'arm64-apple-ios15.0'+('-simulator' if kind == 'simulator' else '')


def inventory(path, exclude=()):
    rows = []
    for p in sorted(path.rglob('*')):
        if any(x in p.relative_to(path).parts for x in exclude):
            continue
        if p.is_symlink():
            raise ValueError('generated framework symlink refused')
        if p.is_file():
            body = read(p)
            rows.append({'path': str(p.relative_to(path)), 'bytes': len(body), 'sha256': digest(body)})
    return rows


def run(c, root, env, slices):
    # These are pinned Flutter engine inputs, not an old OpenIM framework.
    flutter = Path(c['flutter_xcframework'])
    variants = {'device': 'ios-arm64', 'simulator': 'ios-arm64_x86_64-simulator'}
    for kind in ['device', 'simulator']:
        out = root/'link'/kind; put_dir(out)
        core = slices[kind]/'OpenIMCore'
        original_core = read(core)
        for label, argv in [('ar', [c['ar'], '-t', core]), ('file', [c['file'], '-b', core]),
                            ('load', [c['otool'], '-l', core]), ('symbols', [c['nm'], '-g', '--defined-only', core])]:
            command(root, 'core-'+label+'-'+kind, argv, root, env)
            if read(core) != original_core:
                raise ValueError('core changed during actual binary audit')
        evidence = lambda label: (root/'evidence'/('core-'+label+'-'+kind+'.log')).read_text()
        core_audit = audit_core(original_core, kind, evidence('ar'), evidence('file'), evidence('symbols'))
        record(out/'core-audit.json', {'core_sha256': digest(original_core), **core_audit,
               'raw_tools': {label: digest(read(root/'evidence'/('core-'+label+'-'+kind+'.log')))
                             for label in ['ar', 'file', 'load', 'symbols']}})
        sdk = c['apple_sdk'][kind]
        frameworks = ['-F', str(slices[kind].parent), '-F', str(flutter/variants[kind])]
        triple = target(kind)
        swift = Path(c['developer'])/'Toolchains/XcodeDefault.xctoolchain/usr/bin/swiftc'
        clang = Path(c['developer'])/'Toolchains/XcodeDefault.xctoolchain/usr/bin/clang'
        # Same Flutter headers exposed by the mixed-language CocoaPods module.
        put(out/'bridge.h', b'#import <Flutter/Flutter.h>\n')
        sources = [root/'bridge'/x['relative'] for x in c['bridge_files'] if x['relative'].endswith('.swift')]
        common = ['-target', triple, '-sdk', sdk, *frameworks, '-module-cache-path', str(out/'modules')]
        command(root, 'swift-'+kind, [swift, '-emit-object', '-whole-module-optimization', '-parse-as-library',
                '-swift-version', '5', '-module-name', 'flutter_openim_sdk', '-emit-module',
                '-emit-module-path', out/'flutter_openim_sdk.swiftmodule', '-emit-objc-header',
                '-emit-objc-header-path', out/'flutter_openim_sdk-Swift.h', '-import-objc-header', out/'bridge.h',
                *common, *sources, '-o', out/'sdk-swift.o'], root, env)
        command(root, 'objc-shim-'+kind, [clang, '-target', triple, '-isysroot', sdk, '-fobjc-arc', '-fmodules',
                '-fmodules-cache-path='+str(out/'modules'), *frameworks, '-I', out, '-I', root/'bridge',
                '-c', root/'bridge/FlutterOpenimSdkPlugin.m', '-o', out/'sdk-objc.o'], root, env)
        links = [flag for name in ['Flutter', 'OpenIMCore', 'Foundation', 'UIKit', 'AVFoundation',
                                  'SystemConfiguration', 'CoreMedia', 'Security', 'CoreFoundation'] for flag in ['-framework', name]]
        command(root, 'link-sdk-'+kind, [swift, '-emit-library', *common, out/'sdk-swift.o', out/'sdk-objc.o',
                *links, '-lresolv', '-lsqlite3', '-o', out/'libflutter_openim_sdk.dylib'], root, env)
        # Actual Objective-C class + Swift class + new core symbol references;
        # link only. This program is never launched on host/simulator/device.
        put(out/'probe.m', b'#import "FlutterOpenimSdkPlugin.h"\n#import "flutter_openim_sdk-Swift.h"\n#import <OpenIMCore/OpenIMCore.h>\nint main(void) { return ([FlutterOpenimSdkPlugin class] == Nil || [SwiftFlutterOpenimSdkPlugin class] == Nil || Open_im_sdkGetAtAllTag == 0); }\n')
        command(root, 'link-probe-'+kind, [clang, '-target', triple, '-isysroot', sdk, '-fobjc-arc', '-fmodules',
                '-fmodules-cache-path='+str(out/'modules'), *frameworks, '-I', out, '-I', root/'bridge',
                out/'probe.m', '-L', out, '-lflutter_openim_sdk', *links, '-lresolv', '-lsqlite3', '-o', out/'probe'], root, env)
        for name, binary in [('core', slices[kind]/'OpenIMCore'), ('plugin', out/'libflutter_openim_sdk.dylib'), ('probe', out/'probe')]:
            command(root, 'arch-'+kind+'-'+name, [c['lipo'], '-archs', binary], root, env)
            if (root/'evidence'/('arch-'+kind+'-'+name+'.log')).read_text().strip() != 'arm64':
                raise ValueError('exact ARM64 archive/image required')
        command(root, 'macho-'+kind, [c['otool'], '-l', out/'probe'], root, env)
        command(root, 'macho-plugin-'+kind, [c['otool'], '-l', out/'libflutter_openim_sdk.dylib'], root, env)
        probe = macho(read(out/'probe'), kind, MH_EXECUTE)
        plugin = macho(read(out/'libflutter_openim_sdk.dylib'), kind, MH_DYLIB)
        command(root, 'dylibs-'+kind, [c['otool'], '-L', out/'libflutter_openim_sdk.dylib'], root, env)
        deps = plugin_dependencies((root/'evidence'/('dylibs-'+kind+'.log')).read_text(),
                                   out/'libflutter_openim_sdk.dylib', flutter/variants[kind]/'Flutter.framework/Flutter')
        record(out/'linked-binary-audit.json', {'plugin': plugin, 'probe': probe, 'plugin_dependencies': deps,
               'OpenIMCore_dynamic_dependency': False, 'old_framework_path': False})
        record(out/'outputs.json', {'target': triple, 'SDK_only': True, 'executed_programs': 0,
               'files': inventory(out, exclude=('modules',)), 'generated_core_framework': inventory(slices[kind]),
               'framework_declared_MinimumOSVersion': plistlib.loads(read(slices[kind]/'Info.plist')).get('MinimumOSVersion'),
               'official_gomobile_plist_100_workaround_not_a_device_acceptance': True})
    inputs()
    record(root/'link-passed.json', {'targets': ['device', 'simulator'], 'arm64_only': True,
                                   'actual_Swift_ObjC_compile_and_link': True, 'runtime': False})
