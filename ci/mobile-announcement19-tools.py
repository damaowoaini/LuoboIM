#!/usr/bin/env python3
"""Only new scoped overlay, argv and honest prior-success reuse controls."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import unittest

spec = importlib.util.spec_from_file_location('announcement19', Path(__file__).with_name('mobile-announcement19.py'))
focused = importlib.util.module_from_spec(spec)
spec.loader.exec_module(focused)
INPUT = Path(__file__).parent/'announcement19/inputs.json'


class Announcement19Tests(unittest.TestCase):
    def test_exact_two_before_and_payload_reject_drift_missing_or_extra_source(self):
        original = {p: ('old-'+p).encode() for p in focused.SELECTED}
        payload = {p: ('new-'+p).encode() for p in focused.SELECTED}

        def row(p, body):
            return {'path': p, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}

        before = [row(p, original[p]) for p in focused.SELECTED]
        after = [row(p, payload[p]) for p in focused.SELECTED]
        self.assertEqual(focused.overlay_bytes(original, before, after, payload), payload)
        changed = dict(original)
        changed[focused.SELECTED[0]] = b'newer unrelated fixture'
        with self.assertRaisesRegex(ValueError, 'source pin differs'):
            focused.overlay_bytes(changed, before, after, payload)
        with self.assertRaisesRegex(ValueError, 'exactly two'):
            focused.overlay_bytes(original, before, after, {**payload, 'extra.dart': b'extra'})
        with self.assertRaisesRegex(ValueError, 'missing original'):
            focused.overlay_bytes({}, before, after, payload)

    def test_only_changed_two_analysis_and_failed_store_plus_all_eighteen_widgets(self):
        cfg = json.loads(INPUT.read_text())
        focused.validate(cfg)
        analyses, command = focused.phase_commands(Path('/tmp/fresh-announcement19'))
        self.assertEqual([c[-1] for c in analyses], focused.ANALYSIS)
        self.assertEqual(len(analyses), 2)
        self.assertTrue(all(c[1:3] == ['analyze', '--fatal-infos'] for c in analyses))
        self.assertEqual(command[-2:], focused.TESTS)
        pattern = re.compile(command[command.index('--name')+1])
        self.assertTrue(all(pattern.fullmatch(n) for n in focused.NINETEEN))
        self.assertTrue(all(n in focused.NINETEEN for n in focused.BASE.WIDGET_NAMES))
        self.assertIsNone(pattern.fullmatch(focused.SEVEN[0]))
        changed = copy.deepcopy(cfg)
        changed['target']['names'][0] = focused.SEVEN[0]
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate(changed)
        changed = copy.deepcopy(cfg)
        changed['target']['analysis_paths'].append('lib/app.dart')
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate(changed)
        source = "  test('preserved', () {\n    expect(true, isTrue);\n  });\n"
        self.assertEqual(focused.case_block(source.encode(), 'preserved'), source.encode())
        with self.assertRaisesRegex(ValueError, 'not unique'):
            focused.case_block(source.encode(), 'missing')

    def test_prior_false_footer_only_proves_seven_successes_and_rejects_stale_or_failed_reuse(self):
        cfg = json.loads(INPUT.read_text())
        raw = cfg['reuse']['raw_log_utf8']
        summary = focused.successful_subset(raw, focused.BASE.TWENTY_SIX,
                                            focused.FAILED_ORIGINAL, focused.SEVEN)
        self.assertEqual(summary['reused_successes'], 7)
        self.assertTrue(summary['original_false_footer'])
        self.assertTrue(summary['not_a_full26_pass'])
        with self.assertRaisesRegex(ValueError, 'source pin differs'):
            focused.BASE.pinned((raw+'changed').encode(), cfg['reuse']['raw_log_pin'])
        events = []
        for line in raw.splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if isinstance(event, dict):
                events.append(event)
        identity = next(e['test']['id'] for e in events
                        if e.get('type') == 'testStart' and e['test']['name'] == focused.SEVEN[0])
        changed = copy.deepcopy(events)
        next(e for e in changed if e.get('type') == 'testDone' and e['testID'] == identity)['result'] = 'failure'
        with self.assertRaisesRegex(ValueError, 'subset differs'):
            focused.successful_subset('\n'.join(map(json.dumps, changed)), focused.BASE.TWENTY_SIX,
                                      focused.FAILED_ORIGINAL, focused.SEVEN)
        changed = copy.deepcopy(events)
        next(e for e in changed if e.get('type') == 'done')['success'] = True
        with self.assertRaisesRegex(ValueError, 'false footer'):
            focused.successful_subset('\n'.join(map(json.dumps, changed)), focused.BASE.TWENTY_SIX,
                                      focused.FAILED_ORIGINAL, focused.SEVEN)


if __name__ == '__main__':
    unittest.main(verbosity=2)
