"""Account only the exact new xcrun cache leaves admitted by the sealed policy.

Darwin's per-user T directory is shared with runner tools. Its unrelated existing
subtrees are not this command's allocations. The unchanged resource guard still
owns process stop/join, primary work allocation, timeout and global free disk.
"""
import json
import os
from pathlib import Path
import re
import stat


def allocated_cache_leaves(row, check_directory):
    root = Path(row['canonical'])
    if check_directory(root, row['uid']) != row['directory_identity']:
        raise ValueError('cache directory identity changed')
    protected = set(row['protected_preexisting'])
    total = matched = 0
    with os.scandir(root) as entries:
        for number, entry in enumerate(entries):
            if number >= 4096:
                raise ValueError('bounded cache directory inventory required')
            if entry.name in protected or not re.fullmatch(r'xcrun_db-[A-Za-z0-9]{8}', entry.name):
                continue
            matched += 1
            if matched > 256:
                raise ValueError('bounded new tool cache leaves required')
            try:
                observed = entry.stat(follow_symlinks=False)
            except FileNotFoundError:
                continue  # The actual tool may unlink a temporary leaf.
            if (not stat.S_ISREG(observed.st_mode) or observed.st_uid != row['uid']
                    or observed.st_nlink != 1):
                raise ValueError('new cache leaf must be a current-user regular file')
            total += observed.st_blocks * 512
    if check_directory(root, row['uid']) != row['directory_identity']:
        raise ValueError('cache directory identity changed during measurement')
    return total


def run(guard, adapter, work, arguments):
    """One independently loaded guard module, one command, restored on every exit."""
    work = Path(work)
    row = adapter.cache_boundary(work)
    secondary = Path(row['canonical'])
    if (work.resolve(strict=True) != work or Path(arguments.allocated_root) != work
            or Path(arguments.secondary_root) != secondary
            or arguments.secondary_cap_mib != 64 or arguments.min_free_mib < 1024):
        raise ValueError('unchanged work/cache caps and free disk floor required')
    scope = {'schema': 1, 'secondary_allocation_scope': 'new-exact-xcrun-cache-leaves',
             'canonical': str(secondary), 'uid': row['uid'],
             'directory_identity': row['directory_identity'],
             'protected_preexisting_leaf_count': len(row['protected_preexisting']),
             'secondary_cap_bytes': 64 * 1048576,
             'unrelated_shared_subtrees_counted': False,
             'process_exclusive_cache_ownership_claimed': False,
             'file_bodies_read': False, 'files_deleted': False}
    target = work/'evidence/cache-resource-scope.json'
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(scope, output, sort_keys=True, indent=2)
        output.write('\n'); output.flush(); os.fsync(output.fileno())
    original = guard.allocated

    def measured(root):
        if root is not None and Path(root) == secondary:
            return allocated_cache_leaves(row, adapter.cache_directory_identity)
        return original(root)

    # The original guard's lifecycle and every cap comparison stay intact. This
    # module instance is loaded for this synchronous command only; no shared CI
    # source or other guard instance is modified.
    guard.allocated = measured
    try:
        return guard.run(arguments)
    finally:
        guard.allocated = original
