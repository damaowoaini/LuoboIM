#!/usr/bin/env python3
"""Keep the guarded command's actual stdout and stderr in distinct fresh files."""
import argparse
from pathlib import Path
import subprocess


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--stdout', required=True, type=Path)
    p.add_argument('--stderr', required=True, type=Path)
    p.add_argument('command', nargs=argparse.REMAINDER)
    a = p.parse_args()
    command = a.command[1:] if a.command[:1] == ['--'] else a.command
    if not command or a.stdout == a.stderr:
        p.error('command and distinct fresh output files required')
    with a.stdout.open('xb') as out, a.stderr.open('xb') as err:
        result = subprocess.run(command, stdout=out, stderr=err, check=False)
    raise SystemExit(result.returncode)
