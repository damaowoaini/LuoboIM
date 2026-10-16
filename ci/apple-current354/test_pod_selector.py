"""Two prepared filesystem-only controls; never execute Ruby, Pod, brew or gems."""
from pathlib import Path
import tempfile
import unittest

import pod_selector


class PreinstalledPodSelection(unittest.TestCase):
    def fixture(self, root, suffix='3.3.0'):
        prefix, home = root/'opt/homebrew', root/'home'
        prefix.mkdir(parents=True, exist_ok=True); home.mkdir(exist_ok=True)
        ruby = prefix/'Cellar/ruby@3.3/3.3.8/bin/ruby'
        ruby.parent.mkdir(parents=True, exist_ok=True)
        ruby.write_bytes(b'not an executed Ruby binary'); ruby.chmod(0o755)
        gems = prefix/('lib/ruby/gems/'+suffix)
        for name in ['bin', 'gems', 'specifications']:
            (gems/name).mkdir(parents=True, exist_ok=True)
        pod = gems/'bin/pod'
        pod.write_bytes(('#!'+str(ruby)+'\n# inert script fixture\n').encode()); pod.chmod(0o755)
        aliases = (root/'opt/homebrew/bin/pod', root/'usr/local/bin/pod')
        aliases[1].parent.mkdir(parents=True, exist_ok=True)
        if not aliases[1].exists():
            aliases[1].symlink_to(pod)
        return prefix, home, ruby, pod, aliases

    def test_official_compatibility_alias_and_canonical_dedup_remain_bound(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve()
            prefix, home, ruby, pod, aliases = self.fixture(root)
            self.assertFalse(aliases[0].exists())
            proof = pod_selector.select(home, aliases, prefix)
            self.assertEqual(proof['script']['path'], str(pod))
            self.assertEqual(proof['ruby']['path'], str(ruby))
            self.assertEqual(proof['gem_root'], str(pod.parent.parent))
            aliases[0].parent.mkdir(parents=True, exist_ok=True); aliases[0].symlink_to(pod)
            self.assertEqual(pod_selector.select(home, aliases, prefix), proof)
            self.assertEqual(pod_selector.recheck(proof, home, prefix), pod)
            # Once selected, deletion of a convenience alias cannot substitute another tool.
            aliases[1].unlink()
            self.assertEqual(pod_selector.recheck(proof, home, prefix), pod)
            pod.write_bytes(pod.read_bytes()+b'# substituted\n')
            with self.assertRaisesRegex(ValueError, 'changed'):
                pod_selector.recheck(proof, home, prefix)

    def test_unknown_ambiguous_user_or_interpreter_sources_are_refused(self):
        for mode in ['absent', 'ambiguous', 'outside', 'user', 'env-ruby', 'changed-ruby']:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as name:
                root = Path(name).resolve()
                prefix, home, ruby, pod, aliases = self.fixture(root)
                proof = pod_selector.select(home, aliases, prefix)
                if mode == 'absent':
                    aliases[1].unlink()
                elif mode == 'ambiguous':
                    _, _, _, other, _ = self.fixture(root, '3.4.0')
                    aliases[0].parent.mkdir(parents=True, exist_ok=True); aliases[0].symlink_to(other)
                elif mode in {'outside', 'user'}:
                    replacement = (root if mode == 'outside' else home)/'pod'
                    replacement.write_bytes(b'#!/usr/bin/ruby\n'); replacement.chmod(0o755)
                    aliases[1].unlink(); aliases[1].symlink_to(replacement)
                elif mode == 'env-ruby':
                    pod.write_bytes(b'#!/usr/bin/env ruby\n')
                else:
                    ruby.write_bytes(b'substituted Ruby')
                with self.assertRaises((ValueError, FileNotFoundError)):
                    if mode == 'changed-ruby':
                        pod_selector.recheck(proof, home, prefix)
                    else:
                        pod_selector.select(home, aliases, prefix)


if __name__ == '__main__':
    unittest.main()
