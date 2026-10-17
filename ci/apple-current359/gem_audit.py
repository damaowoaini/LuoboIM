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

    def mapped(value, proof, field, subject, exists=True, remember=True):
        p = path(value, field, subject, exists=exists)
        kind = 'file' if p.is_file() else 'directory' if p.is_dir() else 'other' if p.exists() else 'missing'
        observed = {'lexical': value, 'canonical': str(p) if kind != 'missing' else None, 'kind': kind}
        require(isinstance(proof, dict) and proof == observed,
                field+'.mapping', subject, 'probe/path mapping changed or incomplete')
        if remember:
            checked_mappings.append((value, dict(observed), field, subject, exists))
        return p

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
    # Homebrew deliberately does not create site/vendor search dirs on install.
    # Admit only these finite metadata entries; actual loaded files still need
    # the two selected source roots, never a general Homebrew or user exception.
    search_only = {Path('/opt/homebrew/lib/ruby')/kind/suffix
                   for kind in ('site_ruby', 'vendor_ruby')
                   for suffix in ('', '3.3.0', '3.3.0/'+config['arch'], config['arch'])}
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
        require(Path(gem['full_gem_path']) == logical_base/'gems'/full_name and
                computed == expected_base/'gems'/full_name,
                'full_gem_path', name, 'spec/gem must map to one exact selected base/fullname')
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
            require(p == expected_base/logical.relative_to(logical_base) and
                    (p.is_dir() or (default and not p.exists())),
                    'full_require_paths', name, 'spec/gem/require counterpart mapping differs')
            canonical_requires.append(str(p))
        row = {**gem, 'spec_body': spec_body, 'computed_directory_exists': computed.is_dir(),
               'computed_directory_is_source_evidence': not default, 'ruby_sources': [], 'loaded_files': [],
               'canonical_gem_root': str(computed), 'canonical_full_require_paths': canonical_requires}
        if not default:
            require(computed.is_dir(), 'full_gem_path', name, 'real regular gem directory required')
            def walk_error(error):
                raise AuditFailure('source_walk', name, type(error).__name__) from error
            for parent, dirs, paths in os.walk(computed, followlinks=False, onerror=walk_error):
                for entry in dirs+paths:
                    require(not (Path(parent)/entry).is_symlink(), 'source_walk', name, 'gem tree symlink refused')
                for entry in sorted(paths):
                    if entry.endswith('.rb'):
                        row['ruby_sources'].append(body(Path(parent)/entry, 'ruby_source', name))
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
                   any(p.is_relative_to(Path(root)) for root in g['canonical_full_require_paths']))]
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
    return {'schema': 3, 'ruby': source['ruby'], 'rubygems_observed': source['rubygems'],
            'ruby_binary': binary, 'configured_shared_libruby': libruby,
            'libruby_scope': 'RbConfig selected library body; not a dyld process-map claim',
            'config': config, 'gems': specs, 'loaded_features': features,
            'source_bodies': sorted(bodies.values(), key=lambda r: r['path']),
            'preinstalled_source_first_observation': True, 'source_provenance_Root_acceptance': False,
            'scope': 'loaded Ruby/extension/spec bodies plus regular-gem Ruby trees; not arbitrary native transitive dylibs or App acceptance'}
