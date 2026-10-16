"""Explicit read-only recovery of pre-issuer local notes/todos.

Run against an operator-exported personal/v1/<64-hex>.sqlite3 file. Ownership
cannot be inferred from an old tenant/user hash; the user must confirm it in
the destination app's existing archive-import dialog. Never deletes the DB.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import tempfile


def export_database(source, destination):
    source = Path(source).resolve(strict=True)
    if not source.is_file() or not re.fullmatch(r'[a-f0-9]{64}\.sqlite3', source.name):
        raise ValueError('expected an explicitly selected legacy personal database')
    owner = source.stem
    destination = Path(destination).resolve(strict=True)
    folder = Path(tempfile.mkdtemp(prefix='luobo-unscoped-export-', dir=destination))
    os.chmod(folder, 0o700)
    connection = sqlite3.connect(source.as_uri() + '?mode=ro', uri=True)
    parts = []
    current = {'notes': [], 'todos': []}
    current_bytes = 0

    def encoded(value):
        return json.dumps({'format': 'luobo-personal-data', 'version': 1,
                           'applicationId': 'com.luoboduihua.imapp',
                           'owner': owner, **value}, ensure_ascii=False).encode('utf-8')

    def save_part():
        if not current['notes'] and not current['todos']:
            return
        data = encoded(current)
        name = 'personal-part-' + str(len(parts) + 1) + '.json'
        path = folder / name
        with path.open('xb') as output:
            os.chmod(path, 0o600)
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        parts.append({'file': name, 'notes': len(current['notes']),
                      'todos': len(current['todos']), 'bytes': len(data),
                      'sha256': hashlib.sha256(data).hexdigest()})

    try:
        connection.execute('PRAGMA query_only=ON')
        connection.execute('BEGIN')
        if connection.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
            raise ValueError('database integrity check failed; original retained')
        for kind, identifier, payload in connection.execute(
                'SELECT kind,id,payload FROM personal_items ORDER BY kind,id'):
            value = json.loads(payload)
            if kind not in ('note', 'todo') or not isinstance(value, dict) or value.get('id') != identifier:
                raise ValueError('invalid personal record; original retained')
            key = 'notes' if kind == 'note' else 'todos'
            record_bytes = len(json.dumps(value, ensure_ascii=False).encode('utf-8')) + 2
            if record_bytes > 15 * 1024 * 1024 - 4096:
                raise ValueError('single record exceeds supported import size; original retained')
            if current_bytes + record_bytes > 15 * 1024 * 1024 - 4096 or sum(map(len, current.values())) >= 10000:
                save_part()
                current = {'notes': [], 'todos': []}
                current_bytes = 0
            current[key].append(value)
            current_bytes += record_bytes
        save_part()
        manifest = {'state': 'complete', 'ownership': 'unscoped; explicit destination confirmation required',
                    'database': source.name, 'parts': parts,
                    'original': 'read only; not removed or rewritten'}
        path = folder / 'manifest.json'
        with path.open('x', encoding='utf-8') as output:
            os.chmod(path, 0o600)
            json.dump(manifest, output, ensure_ascii=False, indent=2)
            output.flush()
            os.fsync(output.fileno())
        return folder, manifest
    finally:
        connection.close()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database', required=True)
    parser.add_argument('--destination-directory', required=True)
    parser.add_argument('--confirm-unscoped-owner', action='store_true', required=True,
                        help='acknowledge that old records cannot be assigned to a tenant automatically')
    args = parser.parse_args()
    folder, manifest = export_database(args.database, args.destination_directory)
    print(json.dumps({'directory': str(folder), 'partCount': len(manifest['parts'])}))
