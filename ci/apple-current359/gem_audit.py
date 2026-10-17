"""Narrow preinstalled Ruby 3.3.12 / CocoaPods 1.17 loaded-source observation.

Computed default-gem directories are not evidence of installed files. Every
loaded filesystem feature and spec is audited; the only no-file cases are six
official MRI startup markers bound to the selected Ruby and configured libruby.
"""
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import stat



RUBY_COMMIT = '0581089df9f0af0fe6b64cb8167987c211100947'
# gems/bundled_gems at that exact Ruby commit. This is not an arbitrary Cellar
# exemption: even another gem/version in this Ruby tree remains unapproved.
BUNDLED_FULL_NAMES = frozenset('''minitest-5.20.0 power_assert-2.0.3 rake-13.1.0
test-unit-3.6.1 rexml-3.4.4 rss-0.3.1 net-ftp-0.3.4 net-imap-0.4.25
net-pop-0.1.2 net-smtp-0.5.1 matrix-0.4.2 prime-0.1.2 rbs-3.4.0
typeprof-0.21.9 debug-1.9.2 racc-1.7.3'''.split())
BUILTINS = {
    'enumerator.so': ('enumerator.c', 4652),
    'thread.rb': ('thread_sync.c', 1720),
    'fiber.so': ('cont.c', 3566),
    'rational.so': ('rational.c', 2832),
    'complex.so': ('complex.c', 2645),
    'ruby2_keywords.rb': ('version.c', 127),
}


class AuditFailure(ValueError):
    def __init__(self, field, subject, reason):
        self.proof = {'field': field, 'subject': subject, 'reason': reason}
        super().__init__('preinstalled source audit: '+field+': '+subject+': '+reason)


def require(condition, field, subject, reason):
    if not condition:
        raise AuditFailure(field, subject, reason)


def text(value, field, subject, maximum=4096):
    require(isinstance(value, str) and 0 < len(value) <= maximum and
            not any(ord(c) < 32 or ord(c) == 127 for c in value), field, subject, 'bounded text required')
    return value


def values(value, field, subject, maximum):
    require(isinstance(value, list) and len(value) <= maximum, field, subject, 'bounded list required')
    return value


BOOTSTRAP_CONFIG_KEYS = ('sitedir', 'sitelibdir', 'sitearchdir',
               'vendordir', 'vendorlibdir', 'vendorarchdir')


def _bootstrap_require(condition, reason):
    if not condition:
        raise ValueError(reason)


def _bootstrap_identity(s):
    return (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_size,
            s.st_uid, s.st_gid, s.st_mtime_ns, s.st_ctime_ns)


BOOTSTRAP_CODES = frozenset(['configured-abi', 'configured-feature-alias', 'configured-feature-label-mismatch', 'configured-feature-not-bootstrap', 'configured-feature-not-rubygems-ruby', 'configured-feature-outside-library', 'configured-feature-path', 'configured-mappings-missing', 'configured-path-mapping-changed', 'configured-path-outside-selected-layout', 'configured-root-final-changed', 'configured-root-not-directory', 'configured-source-budget', 'configured-source-final-changed', 'configured-source-grew', 'configured-source-leaf', 'configured-source-open-changed', 'configured-source-parent', 'configured-source-read-changed', 'configured-user-root', 'selected-gem-layout', 'selected-owner-missing', 'selected-ruby-alias', 'selected-ruby-owner-changed'])


def _bootstrap_reason(error):
    return str(error) if type(error) is ValueError and str(error) in BOOTSTRAP_CODES else 'configured-source-io'


class ConfiguredRubyGems:
    def __init__(self, source, selected, home):
        self.ruby = Path(selected['ruby']['path'])
        self.owner = (selected['ruby'].get('uid'), selected['ruby'].get('gid'))
        gem = Path(selected['gem_root'])
        _bootstrap_require(gem.is_absolute() and gem.resolve(strict=True) == gem and
                gem.name == '3.3.0' and gem.parent.name == 'gems', 'selected-gem-layout')
        self.base = gem.parent.parent
        self.home = Path(home).resolve(strict=True)
        _bootstrap_require(not self.base.is_relative_to(self.home), 'configured-user-root')
        c = source['config']
        _bootstrap_require(c.get('ruby_version') == '3.3.0' and
                re.fullmatch(r'[A-Za-z0-9_-]+', c.get('arch', '')) is not None,
                'configured-abi')
        self.expected = {}
        for prefix, kind in [('site', 'site_ruby'), ('vendor', 'vendor_ruby')]:
            directory = self.base/kind
            library = directory/c['ruby_version']
            self.expected.update({prefix+'dir': directory, prefix+'libdir': library,
                                  prefix+'archdir': library/c['arch']})
        self.config = {key: c.get(key) for key in BOOTSTRAP_CONFIG_KEYS}
        self.maps = source.get('config_path_mappings')
        _bootstrap_require(isinstance(self.maps, dict) and set(self.maps) == set(BOOTSTRAP_CONFIG_KEYS),
                'configured-mappings-missing')
        self.initial_maps = self.configured_paths()
        self.library_roots = {self.expected[k] for k in
                              ('sitelibdir', 'sitearchdir', 'vendorlibdir', 'vendorarchdir')}
        self.files = {}
        self.total = 0

    def selected_owner(self):
        _bootstrap_require(all(type(v) is int for v in self.owner), 'selected-owner-missing')
        _bootstrap_require(self.ruby.is_absolute() and self.ruby.resolve(strict=True) == self.ruby,
                'selected-ruby-alias')
        s = self.ruby.lstat()
        _bootstrap_require(stat.S_ISREG(s.st_mode) and (s.st_uid, s.st_gid) == self.owner,
                'selected-ruby-owner-changed')

    def configured_paths(self):
        result = {}
        for key in BOOTSTRAP_CONFIG_KEYS:
            p = self.expected[key]
            _bootstrap_require(self.config[key] == str(p) and p.resolve(strict=False) == p,
                    'configured-path-outside-selected-layout')
            exists = p.exists()
            state = p.lstat() if exists else None
            _bootstrap_require(not exists or stat.S_ISDIR(state.st_mode), 'configured-root-not-directory')
            observed = {'lexical': str(p), 'canonical': str(p) if exists else None,
                        'kind': 'directory' if exists else 'missing'}
            _bootstrap_require(self.maps[key] == observed, 'configured-path-mapping-changed')
            result[key] = {'mapping': observed, 'identity': _bootstrap_identity(state) if state else None}
        return result

    def observe(self, path):
        self.selected_owner()
        relative = path.relative_to(self.base)
        descriptors, parents = [], []
        try:
            descriptor = os.open(self.base, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
            descriptors.append(descriptor)
            here = self.base
            for part in (None,)+relative.parts[:-1]:
                if part is not None:
                    descriptor = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                                         dir_fd=descriptor)
                    descriptors.append(descriptor)
                    here = here/part
                s = os.fstat(descriptor)
                _bootstrap_require(stat.S_ISDIR(s.st_mode) and (s.st_uid, s.st_gid) == self.owner and
                        not s.st_mode & 0o022, 'configured-source-parent')
                parents.append((str(here), _bootstrap_identity(s)))
            before = os.stat(relative.name, dir_fd=descriptor, follow_symlinks=False)
            _bootstrap_require(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and
                    (before.st_uid, before.st_gid) == self.owner and not before.st_mode & 0o022 and
                    0 < before.st_size <= 4*1024**2, 'configured-source-leaf')
            opened = os.open(relative.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                             dir_fd=descriptor)
            descriptors.append(opened)
            _bootstrap_require(_bootstrap_identity(os.fstat(opened)) == _bootstrap_identity(before), 'configured-source-open-changed')
            h, count = hashlib.sha256(), 0
            while chunk := os.read(opened, 1024**2):
                count += len(chunk)
                _bootstrap_require(count <= before.st_size, 'configured-source-grew')
                h.update(chunk)
            _bootstrap_require(count == before.st_size and _bootstrap_identity(os.fstat(opened)) == _bootstrap_identity(before) and
                    _bootstrap_identity(os.stat(relative.name, dir_fd=descriptor, follow_symlinks=False)) == _bootstrap_identity(before) and
                    path.resolve(strict=True) == path and _bootstrap_identity(path.lstat()) == _bootstrap_identity(before),
                    'configured-source-read-changed')
            return {'body': {'path': str(path), 'bytes': count, 'sha256': h.hexdigest(),
                             'mode': stat.S_IMODE(before.st_mode)},
                    'identity': _bootstrap_identity(before), 'parents': parents}
        finally:
            for descriptor in reversed(descriptors):
                os.close(descriptor)

    def admit(self, feature, load_path):
        _bootstrap_require(feature.get('bootstrap') is True and isinstance(feature.get('paths'), list) and
                len(feature['paths']) == 1, 'configured-feature-not-bootstrap')
        _bootstrap_require(isinstance(feature['paths'][0], str) and 0 < len(feature['paths'][0]) <= 4096,
                'configured-feature-path')
        p = Path(feature['paths'][0])
        _bootstrap_require(p.is_absolute() and '..' not in p.parts and p.resolve(strict=True) == p,
                'configured-feature-alias')
        matches = [root for root in self.library_roots if p.is_relative_to(root)]
        _bootstrap_require(matches, 'configured-feature-outside-library')
        root = max(matches, key=lambda value: len(value.parts))
        relative = p.relative_to(root).as_posix()
        _bootstrap_require(re.fullmatch(r'rubygems\.rb|rubygems/(?:[A-Za-z0-9_-]+/)*[A-Za-z0-9_-]+\.rb', relative)
                is not None, 'configured-feature-not-rubygems-ruby')
        label = feature.get('feature')
        _bootstrap_require(isinstance(label, str) and (label == str(p) or
                label == relative and str(root) in load_path), 'configured-feature-label-mismatch')
        if str(p) not in self.files:
            record = self.observe(p)
            self.total += record['body']['bytes']
            _bootstrap_require(len(self.files) < 128 and self.total <= 16*1024**2, 'configured-source-budget')
            self.files[str(p)] = record
        return self.files[str(p)]['body']

    def finish(self):
        if self.files:
            self.selected_owner()
        _bootstrap_require(self.configured_paths() == self.initial_maps, 'configured-root-final-changed')
        for value, before in self.files.items():
            _bootstrap_require(self.observe(Path(value)) == before, 'configured-source-final-changed')
        return {'configuration_paths': self.initial_maps, 'sources': sorted(self.files.values(),
                key=lambda row: row['body']['path']), 'loaded_body_count': len(self.files),
                'no_directory_wide_grant': True, 'first_observation_not_official_content_proof': True,
                'final_owner_parents_FD_path_body_rechecked': True}


def audit(source, selected, home, config_pin):
    home = Path(home).resolve(strict=True)
    ruby = Path(selected['ruby']['path']).resolve(strict=True)
    ruby_root = ruby.parent.parent
    gem_root = Path(selected['gem_root']).resolve(strict=True)
    roots = (ruby_root, gem_root)
    require(source.get('schema') == 2 and source.get('ruby') == '3.3.12', 'ruby', 'runtime', 'fixed 3.3.12 mapped probe required')
    text(source.get('rubygems'), 'rubygems', 'runtime', 64)  # observed, not inferred from bundled source

    def path(value, field, subject, exists=True):
        value = text(value, field, subject)
        p = Path(value)
        require(p.is_absolute() and '..' not in p.parts, field, subject, 'absolute normalized source path required')
        try:
            p = p.resolve(strict=exists)
        except OSError as error:
            raise AuditFailure(field, subject, type(error).__name__) from error
        require(any(p.is_relative_to(root) for root in roots) and not p.is_relative_to(home),
                field, subject, 'outside selected Ruby/system gem roots')
        return p

    bodies = {}
    total = 0
    checked_mappings = []
    checked_leaf_aliases = []

    def mapped(value, proof, field, subject, exists=True, remember=True):
        p = path(value, field, subject, exists=exists)
        kind = 'file' if p.is_file() else 'directory' if p.is_dir() else 'other' if p.exists() else 'missing'
        observed = {'lexical': value, 'canonical': str(p) if kind != 'missing' else None, 'kind': kind}
        require(isinstance(proof, dict) and proof == observed,
                field+'.mapping', subject, 'probe/path mapping changed or incomplete')
        if remember:
            checked_mappings.append((value, dict(observed), field, subject, exists))
        return p

    def refuse_tree_link(entry, source_root, subject):
        # Diagnostic only: never read the linked body or admit a symlink. Keep
        # absolute/private targets and raw exception strings out of evidence.
        def relative(value):
            label = value.as_posix()
            if (len(label) <= 512 and not value.is_absolute() and '..' not in value.parts and
                    not any(ord(c) < 32 or 127 <= ord(c) <= 159 or 0xd800 <= ord(c) <= 0xdfff or
                            c in '\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069' for c in label)):
                return label
            return None
        detail = {'entry_relative': relative(entry.relative_to(source_root)),
                  'link_kind': 'symbolic_link', 'target_root': 'unresolved',
                  'target_relative': None, 'target_kind': 'unresolved',
                  'linked_body_read': False}
        try:
            before = entry.lstat()
            if not stat.S_ISLNK(before.st_mode):
                detail['link_kind'] = 'entry_changed'
            else:
                target = entry.resolve(strict=True)
                if target.is_relative_to(home):
                    detail['target_root'] = 'user_home'
                    detail['target_kind'] = 'not_inspected'
                else:
                    approved = [(label, root) for label, root in
                                [('selected_ruby', ruby_root), ('selected_system_gems', gem_root)]
                                if target.is_relative_to(root)]
                    if approved:
                        label, root = approved[0]
                        detail.update(target_root=label, target_relative=relative(target.relative_to(root)),
                                      target_kind='file' if target.is_file() else 'directory' if target.is_dir() else 'other')
                    else:
                        detail.update(target_root='outside_selected_roots', target_kind='not_inspected')
                after = entry.lstat()
                detail['link_stable_during_observation'] = (
                    before.st_dev, before.st_ino, before.st_mode, before.st_ctime_ns) == (
                    after.st_dev, after.st_ino, after.st_mode, after.st_ctime_ns)
        except (OSError, RuntimeError) as error:
            detail['resolution_error_class'] = type(error).__name__
        rejected = AuditFailure('source_walk', subject, 'gem tree symlink refused')
        rejected.proof['link'] = detail
        raise rejected

    def body(p, field, subject, limit=4*1024**2):
        nonlocal total
        p = path(str(p), field, subject)
        if str(p) in bodies:
            return bodies[str(p)]
        before = p.lstat()
        require(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and 0 < before.st_size <= limit,
                field, subject, 'bounded regular single-link source required')
        stable = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_size,
                            s.st_uid, s.st_gid, s.st_mtime_ns, s.st_ctime_ns)
        h = hashlib.sha256()
        with p.open('rb') as stream:
            require(stable(os.fstat(stream.fileno())) == stable(before), field, subject, 'changed on open')
            while chunk := stream.read(1024**2):
                h.update(chunk)
            require(stable(os.fstat(stream.fileno())) == stable(before), field, subject, 'changed while reading')
        require(stable(p.lstat()) == stable(before), field, subject, 'path replaced')
        total += before.st_size
        require(total <= 256*1024**2 and len(bodies) < 20000, field, subject, 'source observation budget')
        row = {'path': str(p), 'bytes': before.st_size, 'sha256': h.hexdigest(),
               'mode': stat.S_IMODE(before.st_mode)}
        bodies[str(p)] = row
        return row

    def leaf_alias(entry, source_root, full_name, subject, remember=True):
        # Only a registered bundle's same-relative file counterpart is eligible.
        # Do not resolve an arbitrary target and then grant the whole Ruby tree.
        nonlocal total
        linked_body_read = False
        def stable(s):
            return (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_size,
                    s.st_uid, s.st_gid, s.st_mtime_ns, s.st_ctime_ns)

        def label(value, maximum):
            return (isinstance(value, str) and 0 < len(value) <= maximum and
                    not any(ord(c) < 32 or 127 <= ord(c) <= 159 or 0xd800 <= ord(c) <= 0xdfff or
                            c in '\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069' for c in value))

        def observe(root, leaf, symbolic=False):
            nonlocal linked_body_read
            relative = leaf.relative_to(root)
            require(relative.parts and '..' not in relative.parts and label(relative.as_posix(), 512),
                    'source_alias', subject, 'bounded relative leaf required')
            owner = (selected['ruby'].get('uid'), selected['ruby'].get('gid'))
            current_ruby = ruby.lstat()
            require(all(type(v) is int for v in owner) and owner == (current_ruby.st_uid, current_ruby.st_gid),
                    'source_alias', subject, 'original selected Ruby owner binding required')
            descriptors, directories = [], []
            try:
                descriptor = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
                descriptors.append(descriptor)
                here = root
                for part in (None,)+relative.parts[:-1]:
                    if part is not None:
                        descriptor = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
                        descriptors.append(descriptor)
                        here = here/part
                    s = os.fstat(descriptor)
                    require(stat.S_ISDIR(s.st_mode) and (s.st_uid, s.st_gid) == owner and not s.st_mode & 0o022,
                            'source_alias', subject, 'selected-owner nonwritable real parents required')
                    directories.append((str(here), s.st_dev, s.st_ino, s.st_mode, s.st_uid, s.st_gid))
                before = os.stat(relative.name, dir_fd=descriptor, follow_symlinks=False)
                require((before.st_uid, before.st_gid) == owner and before.st_nlink == 1,
                        'source_alias', subject, 'selected-owner single-link leaf required')
                if symbolic:
                    require(stat.S_ISLNK(before.st_mode), 'source_alias', subject, 'original alias required')
                    target = os.readlink(relative.name, dir_fd=descriptor)
                    require(label(target, 4096), 'source_alias', subject, 'bounded alias target required')
                    row = target
                else:
                    require(stat.S_ISREG(before.st_mode) and not before.st_mode & 0o022 and 0 < before.st_size <= 4*1024**2,
                            'source_alias', subject, 'bounded nonwritable regular target required')
                    opened = os.open(relative.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=descriptor)
                    descriptors.append(opened)
                    require(stable(os.fstat(opened)) == stable(before), 'source_alias', subject, 'target changed on open')
                    digest, count = hashlib.sha256(), 0
                    linked_body_read = True
                    while chunk := os.read(opened, 1024**2):
                        count += len(chunk)
                        require(count <= before.st_size, 'source_alias', subject, 'target grew while reading')
                        digest.update(chunk)
                    require(count == before.st_size and stable(os.fstat(opened)) == stable(before),
                            'source_alias', subject, 'target changed while reading')
                    row = {'path': str(leaf), 'bytes': count, 'sha256': digest.hexdigest(),
                           'mode': stat.S_IMODE(before.st_mode)}
                require(stable(os.stat(relative.name, dir_fd=descriptor, follow_symlinks=False)) == stable(before),
                        'source_alias', subject, 'leaf replaced')
                return row, stable(before), directories
            finally:
                for descriptor in reversed(descriptors):
                    os.close(descriptor)

        try:
            relative = entry.relative_to(source_root)
            require(full_name in BUNDLED_FULL_NAMES, 'source_alias', subject, 'registered bundled fullname required')
            source_boundary = gem_root if source_root.is_relative_to(gem_root) else ruby_root
            raw, link_state, source_parents = observe(source_boundary, entry, symbolic=True)
            target = Path(os.path.normpath(str(Path(raw) if Path(raw).is_absolute() else entry.parent/raw)))
            counterpart = ruby_root/'lib/ruby/gems/3.3.0/gems'/full_name/relative
            require(target == counterpart, 'source_alias', subject, 'same-fullname same-relative selected counterpart required')
            actual, target_state, target_parents = observe(ruby_root, target)
            # These bodies are a first observation, not an official Ruby tree pin.
            if actual['path'] in bodies:
                require(bodies[actual['path']] == actual, 'source_alias', subject, 'previously observed body changed')
            else:
                total += actual['bytes']
                require(total <= 256*1024**2 and len(bodies) < 20000, 'source_alias', subject, 'source observation budget')
                bodies[actual['path']] = actual
            proof = {'logical': str(entry), 'entry_relative': relative.as_posix(),
                     'symbolic_target': raw, 'canonical_target': str(target),
                     'target_relative_to_selected_ruby': target.relative_to(ruby_root).as_posix(),
                     'full_name': full_name, 'kind': 'same_relative_bundled_regular_file_leaf',
                     'body': actual, 'link_identity': link_state, 'target_identity': target_state,
                     'source_parents': source_parents, 'target_parents': target_parents}
            if remember:
                checked_leaf_aliases.append((entry, source_root, full_name, subject, proof))
            return proof
        except (AuditFailure, OSError, RuntimeError) as error:
            if not remember or linked_body_read:
                # A failed recheck/read is not R6's never-read refused link.
                rejected = AuditFailure('source_alias.mapping' if not remember else 'source_alias',
                                        subject, 'leaf alias observation changed or failed')
                rejected.proof['link'] = {'entry_relative': relative.as_posix() if label(relative.as_posix(), 512) else None,
                                          'linked_body_read': linked_body_read,
                                          'observation': 'final_recheck' if not remember else 'initial_read',
                                          'error_class': type(error).__name__}
                raise rejected from error
            # Preserve R6's bounded rejection evidence for every other link class.
            refuse_tree_link(entry, source_root, subject)

    require(path(source.get('ruby_binary'), 'ruby_binary', 'runtime') == ruby,
            'ruby_binary', 'runtime', 'selected interpreter differs')
    binary = body(ruby, 'ruby_binary', 'runtime', 256*1024**2)
    require(all(binary[k] == selected['ruby'][k] for k in ('path', 'bytes', 'sha256', 'mode')),
            'ruby_binary', 'runtime', 'original selected body differs')
    config = source.get('config')
    require(isinstance(config, dict), 'config', 'runtime', 'RbConfig observation required')
    for key in ('prefix', 'bindir', 'libdir', 'rubylibdir', 'rubyarchdir'):
        p = path(config.get(key), key, 'RbConfig')
        require(p.is_relative_to(ruby_root) and p.is_dir(), key, 'RbConfig', 'selected Ruby directory required')
    require(path(config['prefix'], 'prefix', 'RbConfig') == ruby_root and
            path(config['bindir'], 'bindir', 'RbConfig') == ruby.parent,
            'config', 'runtime', 'selected Ruby prefix/bindir required')
    require(config.get('ruby_version') == '3.3.0' and config.get('ENABLE_SHARED') == 'yes',
            'config', 'runtime', 'fixed Ruby ABI/shared runtime required')
    require(re.fullmatch(r'[A-Za-z0-9_-]+', str(config.get('arch'))) is not None and
            config.get('DLEXT') == 'bundle', 'config', 'runtime', 'fixed macOS extension/arch required')
    library = text(config.get('LIBRUBY_SO'), 'LIBRUBY_SO', 'RbConfig', 128)
    require(re.fullmatch(r'libruby[.0-9]*\.dylib', library) is not None,
            'LIBRUBY_SO', 'RbConfig', 'finite configured libruby basename required')
    libruby = body(Path(config['libdir'])/library, 'libruby', 'runtime', 128*1024**2)
    require(Path(libruby['path']).is_relative_to(ruby_root), 'libruby', 'runtime', 'selected Ruby runtime required')
    default_dir = path(source.get('default_dir'), 'default_dir', 'RubyGems')
    default_specs = path(source.get('default_specifications_dir'), 'default_specifications_dir', 'RubyGems')
    default_base = ruby_root/'lib/ruby/gems/3.3.0'
    require(default_dir == gem_root and default_specs == default_base/'specifications/default',
            'default_specifications_dir', 'RubyGems', 'Ruby default spec root required')
    require(path(source.get('gem_dir'), 'gem_dir', 'RubyGems') == gem_root and
            [path(v, 'gem_path', 'RubyGems') for v in values(source.get('gem_path'), 'gem_path', 'RubyGems', 4)] == [gem_root],
            'gem_path', 'RubyGems', 'selected system GEM_HOME/PATH only')
    # Six paths come from this selected Ruby's actual RbConfig and mappings.
    # Only finite search metadata is admitted here; loaded bodies require the
    # independent bootstrap RubyGems class and source-level checks below.
    try:
        configured = ConfiguredRubyGems(source, selected, home)
    except (ValueError, OSError, RuntimeError) as error:
        reason = _bootstrap_reason(error)
        raise AuditFailure('configured_rubygems', 'RbConfig', reason) from error
    search_only = set(configured.expected.values()) | {
        configured.expected[key]/config['arch'] for key in ('sitedir', 'vendordir')}
    for value in values(source.get('load_path'), 'load_path', 'runtime', 128):
        p = Path(text(value, 'load_path', 'runtime'))
        require(p.is_absolute() and '..' not in p.parts, 'load_path', 'runtime', 'absolute search path required')
        if p in search_only:
            require(p.resolve(strict=False) == p, 'load_path', 'runtime', 'search-only alias refused')
        else:
            path(value, 'load_path', 'runtime', exists=False)

    specs = []
    names = set()
    for gem in values(source.get('gems'), 'gems', 'runtime', 256):
        name = text(gem.get('name'), 'name', 'gem', 128)
        require(re.fullmatch(r'[A-Za-z0-9_.-]+', name) is not None and name not in names,
                'name', name, 'unique gem name required')
        names.add(name)
        version = text(gem.get('version'), 'version', name, 128)
        full_name = text(gem.get('full_name'), 'full_name', name, 256)
        require(re.fullmatch(re.escape(name+'-'+version)+r'(?:-[A-Za-z0-9_.-]+)?', full_name) is not None,
                'full_name', name, 'exact gem/version/platform name required')
        mappings = gem.get('path_mappings')
        require(isinstance(mappings, dict) and set(mappings) == {'spec', 'gem', 'requires'},
                'path_mappings', name, 'explicit spec/gem/require mappings required')
        spec = mapped(gem.get('loaded_from'), mappings['spec'], 'loaded_from', name)
        spec_body = body(spec, 'loaded_from', name)
        default = gem.get('default')
        require(type(default) is bool and default == (spec.parent == default_specs),
                'default', name, 'default flag/spec location mismatch')
        require(spec.name == full_name+'.gemspec', 'loaded_from', name, 'exact spec filename required')
        logical_spec = Path(gem['loaded_from'])
        spec_dir = 'specifications/default' if default else 'specifications'
        allowed_bases = (default_base,) if default else ((gem_root, default_base) if full_name in BUNDLED_FULL_NAMES else (gem_root,))
        logical_bases = [base for base in allowed_bases if logical_spec == base/spec_dir/(full_name+'.gemspec')]
        canonical_bases = [base for base in allowed_bases if spec == base/spec_dir/(full_name+'.gemspec')]
        require(len(logical_bases) == len(canonical_bases) == 1,
                'loaded_from', name, 'exact same-fullname selected spec layout required')
        logical_base, expected_base = logical_bases[0], canonical_bases[0]
        require(logical_base == expected_base or (logical_base == gem_root and expected_base == default_base),
                'loaded_from', name, 'only system-to-selected-Ruby bundled counterpart allowed')
        computed = mapped(gem.get('full_gem_path'), mappings['gem'], 'full_gem_path', name, exists=not default)
        # RubyGems4.0.20 load/base_dir/gems_dir use lexical loaded_from. An exact
        # global spec alias may therefore refer to the selected bundled spec
        # while gem+require sources stay together in the real system gem base.
        hybrid = (not default and full_name in BUNDLED_FULL_NAMES and logical_base == gem_root and
                  expected_base == default_base and computed == gem_root/'gems'/full_name)
        source_base = gem_root if hybrid else expected_base
        require(Path(gem['full_gem_path']) == logical_base/'gems'/full_name and
                computed == source_base/'gems'/full_name,
                'full_gem_path', name, 'exact permitted spec/gem counterpart or hybrid layout required')
        require(computed.is_dir() or (default and not computed.exists()),
                'full_gem_path', name, 'directory or absent default metadata required')
        req = values(gem.get('require_paths'), 'require_paths', name, 16)
        files = values(gem.get('files'), 'files', name, 20000)
        for value in req+files:
            value = text(value, 'spec paths', name)
            p = PurePosixPath(value)
            require(not p.is_absolute() and '..' not in p.parts, 'spec paths', name, 'relative safe spec paths required')
        required = values(gem.get('full_require_paths'), 'full_require_paths', name, 16)
        required_maps = values(mappings['requires'], 'full_require_paths.mapping', name, 16)
        require(len(required) == len(required_maps), 'full_require_paths.mapping', name, 'complete mapping list required')
        canonical_requires = []
        for value, observed in zip(required, required_maps):
            p = mapped(value, observed, 'full_require_paths', name, exists=not default)
            logical = Path(value)
            extension = logical.is_relative_to(logical_base/'extensions') and logical.name == full_name and len(logical.relative_to(logical_base/'extensions').parts) == 3
            require(logical.is_relative_to(logical_base/'gems'/full_name) or extension,
                    'full_require_paths', name, 'foreign require path refused')
            require(p == source_base/logical.relative_to(logical_base) and
                    (p.is_dir() or (default and not p.exists())),
                    'full_require_paths', name, 'spec/gem/require counterpart mapping differs')
            canonical_requires.append(str(p))
        row = {**gem, 'spec_body': spec_body, 'computed_directory_exists': computed.is_dir(),
               'computed_directory_is_source_evidence': not default, 'ruby_sources': [], 'loaded_files': [],
               'file_leaf_aliases': [],
               'canonical_gem_root': str(computed), 'canonical_full_require_paths': canonical_requires,
               'canonical_spec_base': str(expected_base), 'canonical_source_base': str(source_base),
               'layout': 'selected_bundled_spec_with_system_sources' if hybrid else 'same_canonical_base'}
        if not default:
            require(computed.is_dir(), 'full_gem_path', name, 'real regular gem directory required')
            def walk_error(error):
                raise AuditFailure('source_walk', name, type(error).__name__) from error
            for parent, dirs, paths in os.walk(computed, followlinks=False, onerror=walk_error):
                aliases = {}
                for entry in dirs+paths:
                    if (Path(parent)/entry).is_symlink():
                        if entry in dirs:
                            refuse_tree_link(Path(parent)/entry, computed, name)
                        observed = leaf_alias(Path(parent)/entry, computed, full_name, name)
                        row['file_leaf_aliases'].append(observed)
                        aliases[entry] = observed['body']
                for entry in sorted(paths):
                    if entry.endswith('.rb'):
                        row['ruby_sources'].append(aliases[entry] if entry in aliases else body(Path(parent)/entry, 'ruby_source', name))
            require(row['ruby_sources'], 'ruby_source', name, 'nondefault gem cannot claim zero Ruby sources')
        specs.append(row)
    require({'cocoapods', 'cocoapods-core'} <= names and
            all(g['version'] == '1.17.0' and not g['default'] for g in specs if g['name'] in {'cocoapods', 'cocoapods-core'}),
            'version', 'cocoapods', 'strict regular 1.17.0 gems required')

    features, seen = [], set()
    for feature in values(source.get('features'), 'features', 'runtime', 4096):
        label = text(feature.get('feature'), 'feature', 'runtime')
        require(label not in seen, 'feature', label, 'duplicate loaded feature refused')
        seen.add(label)
        paths = values(feature.get('paths'), 'feature.paths', label, 16)
        if label in BUILTINS and feature.get('bootstrap') is True:
            candidates = []
            for value in paths:
                p = path(value, 'marker.search_candidates', label)
                require(any((Path(root)/label).resolve(strict=False) == p for root in source['load_path']),
                        'marker.search_candidates', label, 'unmapped candidate refused')
                candidates.append(str(p))
            features.append({'feature': label, 'kind': 'MRI_compiled_startup_marker',
                             'official_commit': RUBY_COMMIT, 'official_source': BUILTINS[label],
                             'search_candidates_not_claimed_loaded': candidates,
                             'body_binding': [binary['sha256'], libruby['sha256']]})
            continue
        require(len(paths) == 1, 'feature.paths', label, 'one real loaded source required; unknown marker refused')
        if (not any(Path(paths[0]).is_relative_to(root) for root in roots) and
                any(Path(paths[0]).is_relative_to(root) for root in configured.library_roots)):
            try:
                loaded = configured.admit(feature, source['load_path'])
            except (ValueError, OSError, RuntimeError) as error:
                reason = _bootstrap_reason(error)
                raise AuditFailure('configured_rubygems', label, reason) from error
            features.append({'feature': label, 'kind': 'filesystem',
                             'owner': 'selected_Ruby_configured_bootstrap_RubyGems', 'body': loaded})
            continue
        p = path(paths[0], 'feature.paths', label)
        if Path(label).is_absolute():
            require(path(label, 'feature', label) == p, 'feature', label, 'reported file differs')
        else:
            require(not label.startswith(('/', '.')) and '..' not in PurePosixPath(label).parts and
                    any((Path(root)/label).resolve(strict=False) == p for root in source['load_path']),
                    'feature', label, 'relative feature not mapped to approved load path')
        loaded = body(p, 'feature', label, 32*1024**2)
        owners = [g for g in specs if not g['default'] and
                  (p.is_relative_to(Path(g['canonical_gem_root'])) or
                   any(p.is_relative_to(Path(root)) for root in g['canonical_full_require_paths']) or
                   any(str(p) == alias['canonical_target'] for alias in g['file_leaf_aliases']))]
        require(len(owners) <= 1, 'feature.owner', label, 'ambiguous gem ownership')
        if owners:
            owner = owners[0]
            owner['loaded_files'].append(loaded['path'])
            origin = owner['name']
        else:
            require(p.is_relative_to(ruby_root), 'feature.owner', label, 'loaded system-gem file has no exact activated owner')
            # Default spec inventories can be prefixed or unprefixed (Gem.register_default_spec).
            for g in specs:
                if not g['default']:
                    continue
                prefixes = [r+'/' for r in g['require_paths']]
                new_format = any(f.startswith(prefix) for f in g['files'] for prefix in prefixes)
                for f in g['files']:
                    relative = next((f[len(prefix):] for prefix in prefixes if f.startswith(prefix)), None) if new_format else f
                    if relative is None:
                        continue
                    variants = {relative}
                    if relative.endswith('.so'):
                        variants.add(relative[:-3]+'.'+str(config.get('DLEXT')))
                    if any(p == (Path(config[root])/v).resolve(strict=False)
                           for root in ('rubylibdir', 'rubyarchdir') for v in variants):
                        g['loaded_files'].append(loaded['path'])
            origin = 'selected_Ruby_stdlib_or_default_shared_root'
        features.append({'feature': label, 'kind': 'filesystem', 'owner': origin, 'body': loaded})
    require(any(f['kind'] == 'filesystem' and f['body']['path'].endswith('/rbconfig.rb') for f in features),
            'rbconfig', 'runtime', 'actual loaded configuration source required')
    require(any(f['kind'] == 'filesystem' and f['body']['path'].endswith('/rubygems.rb') for f in features),
            'rubygems', 'runtime', 'actual loaded RubyGems source required')
    config_body = body(gem_root/'gems/cocoapods-1.17.0/lib/cocoapods/config.rb', 'config', 'cocoapods')
    require(all(config_body[k] == config_pin[k] for k in ('bytes', 'sha256')),
            'config', 'cocoapods', 'immutable official source differs')
    for g in specs:
        g['loaded_files'] = sorted(set(g['loaded_files']))
        g['activation_only_no_individual_loaded_file'] = not g['loaded_files']
    for value, observed, field, subject, exists in checked_mappings:
        mapped(value, observed, field, subject, exists=exists, remember=False)
    for entry, source_root, full_name, subject, observed in checked_leaf_aliases:
        require(leaf_alias(entry, source_root, full_name, subject, remember=False) == observed,
                'source_alias.mapping', subject, 'leaf alias/body/parent identity changed during audit')
    try:
        configured_proof = configured.finish()
    except (ValueError, OSError, RuntimeError) as error:
        reason = _bootstrap_reason(error)
        raise AuditFailure('configured_rubygems', 'final_recheck', reason) from error
    configured_bodies = [row['body'] for row in configured_proof['sources']]
    require(total + sum(row['bytes'] for row in configured_bodies) <= 256*1024**2 and
            len(bodies) + len(configured_bodies) < 20000,
            'configured_rubygems', 'budget', 'source observation budget')
    return {'schema': 6, 'ruby': source['ruby'], 'rubygems_observed': source['rubygems'],
            'configured_bootstrap_RubyGems': configured_proof,
            'ruby_binary': binary, 'configured_shared_libruby': libruby,
            'libruby_scope': 'RbConfig selected library body; not a dyld process-map claim',
            'config': config, 'gems': specs, 'loaded_features': features,
            'source_bodies': sorted(list(bodies.values()) + configured_bodies, key=lambda r: r['path']),
            'preinstalled_source_first_observation': True, 'source_provenance_Root_acceptance': False,
            'scope': 'loaded Ruby/extension/spec bodies plus regular-gem Ruby trees; not arbitrary native transitive dylibs or App acceptance'}
