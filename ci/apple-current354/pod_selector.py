"""Finite preinstalled CocoaPods selection; no PATH search, subprocess or installation."""
import hashlib
import os
from pathlib import Path
import re
import stat


ALIASES = (Path('/opt/homebrew/bin/pod'), Path('/usr/local/bin/pod'))
PREFIX = Path('/opt/homebrew')
SCHEMA = 'current354-preinstalled-pod-v1'


def _canonical(path, prefix, home):
    path = Path(path)
    if not path.is_absolute():
        raise ValueError('absolute native tool path required')
    result = path.resolve(strict=True)
    if not result.is_relative_to(prefix) or result.is_relative_to(home):
        raise ValueError('unknown or user native tool source refused')
    return result


def _file(path, limit):
    before = path.lstat()
    if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1
            or not 0 < before.st_size <= limit or not before.st_mode & 0o111
            or before.st_mode & 0o022):
        raise ValueError('bounded nonwritable preinstalled executable required')
    def stable(value):
        return (value.st_dev, value.st_ino, value.st_mode, value.st_nlink, value.st_size,
                value.st_uid, value.st_gid, value.st_mtime_ns, value.st_ctime_ns)
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        if stable(os.fstat(stream.fileno())) != stable(before):
            raise ValueError('native tool changed while opening')
        while body := stream.read(1024**2):
            digest.update(body)
        if stable(os.fstat(stream.fileno())) != stable(before):
            raise ValueError('native tool changed while reading')
    if stable(path.lstat()) != stable(before):
        raise ValueError('native tool path replaced while reading')
    return {'path': str(path), 'bytes': before.st_size, 'sha256': digest.hexdigest(),
            'mode': stat.S_IMODE(before.st_mode), 'device': before.st_dev,
            'inode': before.st_ino, 'uid': before.st_uid, 'gid': before.st_gid}


def inspect(pod, home, prefix=PREFIX):
    """Inspect one canonical target, including its absolute Ruby and system gem root."""
    home = Path(home).resolve(strict=True)
    prefix = Path(prefix).resolve(strict=True)
    pod = _canonical(pod, prefix, home)
    relative = pod.relative_to(prefix).as_posix()
    # Official image uses Homebrew Ruby's gem executable directory. Homebrew's
    # CocoaPods formula can instead expose a libexec/bin/pod wrapper.
    if not re.fullmatch(r'(?:lib/ruby/gems/[0-9]+\.[0-9]+\.[0-9]+|'
                        r'Cellar/ruby(?:@[0-9]+\.[0-9]+)?/[^/]+/lib/ruby/gems/[0-9]+\.[0-9]+\.[0-9]+|'
                        r'Cellar/cocoapods/[^/]+/libexec)/bin/pod', relative):
        raise ValueError('unreviewed preinstalled CocoaPods layout')
    script = _file(pod, 64*1024)
    header = pod.read_bytes().splitlines()[0].decode('ascii')
    if not header.startswith('#!/') or len(header.split()) != 1:
        raise ValueError('absolute argument-free Ruby interpreter required')
    ruby = _canonical(Path(header[2:]), prefix, home)
    if not re.fullmatch(r'Cellar/ruby(?:@[0-9]+\.[0-9]+)?/[^/]+/bin/ruby',
                        ruby.relative_to(prefix).as_posix()):
        raise ValueError('unreviewed preinstalled Ruby layout')
    interpreter = _file(ruby, 256*1024**2)
    if _file(pod, 64*1024) != script:
        raise ValueError('CocoaPods script changed while resolving Ruby')
    gem_root = _canonical(pod.parent.parent, prefix, home)
    for name in ('gems', 'specifications'):
        child = gem_root/name
        if child.is_symlink() or not child.is_dir():
            raise ValueError('real controlled system gem root required')
    return {'schema': SCHEMA, 'script': script, 'ruby': interpreter,
            'gem_root': str(gem_root)}


def select(home, aliases=ALIASES, prefix=PREFIX):
    """Only two reviewed aliases; aliases of one target are not ambiguity."""
    home = Path(home).resolve(strict=True)
    prefix = Path(prefix).resolve(strict=True)
    targets = {}
    for alias in aliases:
        alias = Path(alias)
        try:
            alias.lstat()
        except FileNotFoundError:
            continue
        target = _canonical(alias, prefix, home)  # broken/foreign aliases fail closed
        proof = inspect(target, home, prefix)
        targets[str(target)] = proof
    if len(targets) != 1:
        raise ValueError('one unambiguous reviewed preinstalled CocoaPods target required')
    return next(iter(targets.values()))


def recheck(proof, home, prefix=PREFIX):
    """Use the first selected canonical tool; never discover a replacement later."""
    if not isinstance(proof, dict) or proof.get('schema') != SCHEMA:
        raise ValueError('original CocoaPods selection receipt required')
    actual = inspect(Path(proof['script']['path']), home, prefix)
    if actual != proof:
        raise ValueError('original selected CocoaPods or Ruby body changed')
    return Path(proof['script']['path'])


def require_identity(proof, wanted):
    """The reviewed1.17.0 host observation is exact, never a version range."""
    if (not isinstance(proof, dict) or proof.get('schema') != SCHEMA
            or wanted.get('version') != '1.17.0'
            or proof.get('gem_root') != wanted.get('gem_root')):
        raise ValueError('reviewed CocoaPods1.17.0 identity required')
    for kind in ['script', 'ruby']:
        expected = wanted[kind]
        actual = proof[kind]
        if set(expected) != {'path', 'bytes', 'sha256', 'mode'} or any(
                actual.get(key) != expected[key] for key in expected):
            raise ValueError('observed CocoaPods or Ruby identity differs')
