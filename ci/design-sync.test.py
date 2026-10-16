import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('design_sync', ROOT / 'ci/design-sync.py')
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
MOBILE = (ROOT / 'app/lib/design/tokens.dart').read_text()
WEB = (ROOT / 'web/src/design/tokens.ts').read_text()


class SharedDesignTests(unittest.TestCase):
    def test_current_actual_sources_match(self):
        self.assertEqual(len(gate.check(MOBILE, WEB)['compared']), 10)

    def test_mobile_color_change_requires_web_change(self):
        with self.assertRaisesRegex(ValueError, 'design_tokens_differ'):
            gate.check(MOBILE.replace('0xff2563eb', '0xff123456'), WEB)

    def test_web_dimension_change_is_reported(self):
        with self.assertRaisesRegex(ValueError, 'primaryButtonHeight'):
            gate.check(MOBILE, WEB.replace('primaryButtonHeight: 52', 'primaryButtonHeight: 48'))

    def test_matching_reviewed_palette_update_is_supported(self):
        result = gate.check(MOBILE.replace('0xff2563eb', '0xff123456'), WEB.replace('#2563eb', '#123456'))
        self.assertEqual(result['compared']['primary'], '#123456')

    def test_unknown_mobile_structure_fails_for_review(self):
        with self.assertRaisesRegex(ValueError, 'review'):
            gate.check(MOBILE.replace('BorderRadius.circular(12)', 'BorderRadius.circular(radius)'), WEB)


if __name__ == '__main__':
    unittest.main()
