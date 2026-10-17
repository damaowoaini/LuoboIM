"""Only pure argv/path admission. No xcrun/Xcode/Flutter binary is invoked."""
import os
from pathlib import Path
import tempfile
import unittest

import xcrun_adapter as adapter


class OwnedXcrunArguments(unittest.TestCase):
    def test_real_tool_forwarding_and_owned_project_paths_refuse_conflicts(self):
        with tempfile.TemporaryDirectory() as temp:
            work = Path(temp).resolve()
            ios = work/'source/app/ios'
            project = ios/'Runner.xcodeproj'; project.mkdir(parents=True)
            workspace = ios/'Runner.xcworkspace'; workspace.mkdir()
            pods = ios/'Pods/Pods.xcodeproj'; pods.mkdir(parents=True)
            (ios/'Other.xcodeproj').mkdir()
            cloned = ['-clonedSourcePackagesDirPath', str(work/'source/app/build/ios/SourcePackages')]
            caches = ['-derivedDataPath', str(work/'xcode/DerivedData'),
                      '-packageCachePath', str(work/'xcode/PackageCache')]
            variants = [
                ('implicit-resolve', ['xcodebuild', *cloned, '-resolvePackageDependencies'], ios, True),
                ('explicit-project', ['xcodebuild', *cloned, '-skipPackagePluginValidation',
                    '-skipPackageSignatureValidation', '-project', str(project), '-scheme', 'Runner',
                    '-configuration', 'Debug', '-destination', 'generic/platform=iOS',
                    '-showBuildSettings', 'BUILD_DIR='+str(work/'source/app/build/ios')], project, True),
                ('explicit-workspace', ['xcodebuild', *cloned, '-workspace', str(workspace), '-list'], ios, True),
                ('exact-cache-present', ['xcodebuild', *cloned, '-project', str(project), '-resolvePackageDependencies', *caches], ios, True),
                ('implicit-list', ['xcodebuild', *cloned, '-list'], ios, True),
                ('pods-all-targets', ['xcodebuild', '-alltargets', '-sdk', 'iphonesimulator',
                    '-project', str(pods), '-showBuildSettings', 'BUILD_DIR='+str(work/'source/app/build/ios'),
                    'OBJROOT='+str(work/'source/app/build/ios')], pods, True),
                ('target-settings', ['xcodebuild', *cloned, '-project', str(project), '-target', 'Runner',
                    '-showBuildSettings', 'BUILD_DIR='+str(work/'source/app/build/ios')], project, True),
                ('version', ['xcodebuild', '-version'], work, True),
                ('sdk-query', ['--sdk', 'iphoneos', '--show-sdk-path'], work, True),
                ('clang-query', ['clang'], work, True),
                ('device-query', ['xcdevice', 'list', '--timeout', '2'], work, True),
                ('unknown-tool', ['unknown', '--version'], ios, False),
                ('external-project', ['xcodebuild', '-project', '/external/Runner.xcodeproj', '-list'], ios, False),
                ('unproven-owned-project', ['xcodebuild', '-project', str(ios/'Other.xcodeproj'), '-list'], ios, False),
                ('external-cache', ['xcodebuild', *cloned, '-list', '-packageCachePath', '/external/cache'], ios, False),
                ('conflicting-cache', ['xcodebuild', *cloned, '-list', *caches, '-derivedDataPath', str(work/'other')], ios, False),
                ('unknown-setting', ['xcodebuild', *cloned, '-list', 'SECRET=value'], ios, False),
                ('outside-ios-cwd', ['xcodebuild', *cloned, '-list'], work, False),
                ('missing-original-clone', ['xcodebuild', '-resolvePackageDependencies'], ios, False),
                ('unknown-operation', ['xcodebuild', '-project', str(project), 'build'], ios, False),
                ('list-cache-injection', ['xcodebuild', *cloned, '-list', *caches], ios, False),
                ('pods-scheme-conflict', ['xcodebuild', '-alltargets', '-project', str(pods),
                    '-scheme', 'Runner', '-showBuildSettings'], pods, False),
                ('compiler-not-query', ['clang', '-c', 'file.c'], work, False),
            ]
            extras = {'implicit-resolve': ['-scheme', 'Runner', *caches],
                      'explicit-project': caches, 'exact-cache-present': ['-scheme', 'Runner']}
            for name, original, cwd, allowed in variants:
                with self.subTest(case=name):
                    if not allowed:
                        with self.assertRaises(ValueError): adapter.forwarding(work, original, cwd)
                        continue
                    actual, public = adapter.forwarding(work, original, cwd)
                    expected = [adapter.REAL_XCRUN, '--no-cache', *original, *extras.get(name, [])]
                    self.assertEqual(actual, expected)
                    self.assertEqual(actual[0], '/usr/bin/xcrun')
                    self.assertEqual(actual[2:2+len(original)], original)
                    self.assertNotIn(str(work), '\n'.join(public))
                    self.assertNotIn(str(Path(os.environ['HOME'])), '\n'.join(public))
            link = ios/'Borrowed.xcodeproj'; link.symlink_to(project, target_is_directory=True)
            with self.subTest(case='symlink-project'):
                with self.assertRaises(ValueError):
                    adapter.forwarding(work, ['xcodebuild', '-project', str(link), '-list'], ios)


if __name__ == '__main__':
    unittest.main(verbosity=2)
