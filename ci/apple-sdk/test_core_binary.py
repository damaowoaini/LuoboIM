"""Synthetic binary/negative parser controls, not native compilation evidence."""
import struct
import unittest

from core_binary import ARM64, MH_DYLIB, MH_OBJECT, audit_core, macho, plugin_dependencies, thin_archive


def image(kind=2, minimum=15 << 16, filetype=MH_OBJECT, version=True):
    command = struct.pack('<6I', 0x32, 24, kind, minimum, 27 << 16, 0) if version else b''
    return struct.pack('<8I', 0xFEEDFACF, ARM64, 0, filetype, bool(version), len(command), 0, 0)+command


def archive(parts):
    data = b'!<arch>\n'
    for name, body in parts:
        header = f'{name+"/":<16}{0:<12}{0:<6}{0:<6}{100644:<8}{len(body):<10}`\n'.encode()
        assert len(header) == 60
        data += header+body+(b'\n' if len(body) % 2 else b'')
    return data


SYMBOLS = '000000 T _Open_im_sdkInitSDK\n000001 T _Open_im_sdkGetAtAllTag\n'


class CoreBinaryControls(unittest.TestCase):
    def test_actual_binary_parser_classifies_unversioned_without_claiming_all(self):
        body = archive([('bridge.o', image()), ('go.o', image(version=False))])
        result = audit_core(body, 'device', 'bridge.o\ngo.o\n', 'current ar archive', SYMBOLS)
        self.assertEqual(result['versioned_objects'], 1)
        self.assertEqual(result['unversioned_objects'], 1)
        self.assertFalse(result['all_members_platform_proven'])

    def test_core_wrong_platform_and_excess_minimum_are_refused(self):
        for body in [image(kind=7), image(minimum=15 << 16 | 1), image(kind=1)]:
            with self.subTest(body=body.hex()):
                with self.assertRaisesRegex(ValueError, 'platform|minimum'):
                    audit_core(archive([('bridge.o', body)]), 'device', 'bridge.o', 'current ar archive', SYMBOLS)

    def test_static_archive_cannot_be_dylib_or_hide_nonobject(self):
        with self.assertRaisesRegex(ValueError, 'archive'):
            thin_archive(image(filetype=MH_DYLIB))
        for body in [image(filetype=MH_DYLIB), b'unknown assembly data']:
            with self.assertRaises(ValueError):
                audit_core(archive([('fake.o', body)]), 'device', 'fake.o', 'current ar archive', SYMBOLS)

    def test_real_core_version_proof_required_not_lipo_or_filename(self):
        with self.assertRaisesRegex(ValueError, 'actual core object'):
            audit_core(archive([('go.o', image(version=False))]), 'device', 'go.o', 'current ar archive', SYMBOLS)

    def test_ar_file_and_nm_outputs_are_not_ignored(self):
        body = archive([('bridge.o', image())])
        for names, description, symbols in [('other.o', 'current ar archive', SYMBOLS),
                                            ('bridge.o', 'Mach-O dynamically linked library', SYMBOLS),
                                            ('bridge.o', 'current ar archive', SYMBOLS.replace(' T ', ' D '))]:
            with self.assertRaises(ValueError):
                audit_core(body, 'device', names, description, symbols)

    def test_plugin_checks_its_own_platform_and_minimum(self):
        self.assertEqual(macho(image(kind=7, filetype=MH_DYLIB), 'simulator', MH_DYLIB)['classification'],
                         'platform_and_minimum_verified')
        for body in [image(kind=2, filetype=MH_DYLIB), image(kind=7, minimum=16 << 16, filetype=MH_DYLIB),
                     image(kind=7, filetype=MH_DYLIB, version=False)]:
            with self.assertRaises(ValueError):
                macho(body, 'simulator', MH_DYLIB)

    def test_openim_dynamic_and_old_absolute_dependencies_refused(self):
        def listing(paths):
            return '/owned/plugin.dylib:\n'+''.join('  '+p+' (compatibility version 1.0.0, current version 1.0.0)\n' for p in paths)
        good = ['@rpath/libflutter_openim_sdk.dylib', '@rpath/Flutter.framework/Flutter',
                '/System/Library/Frameworks/Foundation.framework/Foundation', '/usr/lib/libsqlite3.dylib',
                '@rpath/libswift_Concurrency.dylib']
        self.assertEqual(plugin_dependencies(listing(good), '/owned/plugin.dylib', '/pinned/Flutter'), good)
        for bad in ['@rpath/OpenIMCore.framework/OpenIMCore', '/old/Framework/OpenIMCore.framework/OpenIMCore',
                    '/old/libOther.dylib', '/usr/lib/../../old/libOther.dylib']:
            with self.assertRaises(ValueError):
                plugin_dependencies(listing(good+[bad]), '/owned/plugin.dylib', '/pinned/Flutter')

    def test_fat_archive_requires_one_real_arm64_archive_slice(self):
        body = archive([('bridge.o', image())])
        fat = b'\xca\xfe\xba\xbe'+struct.pack('>I5I', 1, ARM64, 0, 28, len(body), 0)+body
        self.assertEqual(thin_archive(fat), body)
        with self.assertRaises(ValueError):
            thin_archive(fat[:4]+struct.pack('>I', 2)+fat[8:])


if __name__ == '__main__':
    unittest.main()
