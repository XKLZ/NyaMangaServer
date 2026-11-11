#!/usr/bin/env python3
"""Audit tracked public paths and content. Print paths/rule names, never secret values.

--commit checks every commit reachable from that public ref (not local archives).
This deterministic gate complements review; it is not a general secret detector.
"""
import argparse
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
DENIED_PATH = re.compile(r'(^|/)(\.runtime|\.git|node_modules|secrets|credentials|backups|__pycache__)(/|$)|\.(?:sqlite3?|db|onnx|zip|tar|gz|mp4|pyc)$', re.I)
RULES = {
    'private-home': re.compile(rb'/' + rb'Users/(?!example(?:/|\b))[^/\s]+/'),
    'private-volume': re.compile(rb'/' + rb'Volumes/(?!example(?:/|\b))[^/\s]+/'),
    'private-key': re.compile(rb'-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----'),
    'github-token': re.compile(rb'gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{50,}'),
}


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT)


def audit(ref=None):
    errors = []
    revisions = git('rev-list', ref).decode().splitlines() if ref else [None]
    seen = set()
    count = 0
    for revision in revisions:
        if revision:
            rows = git('ls-tree', '-r', '-z', revision).split(b'\0')
            files = []
            for row in filter(None, rows):
                metadata, name = row.split(b'\t', 1)
                mode, kind, identity = metadata.split()
                files.append((name.decode(), identity.decode(), mode.decode()))
        else:
            files = [(p, None, None) for p in git('ls-files', '-z').decode().split('\0') if p]
        for name, identity, mode in files:
            key = (name, identity)
            if key in seen:
                continue
            seen.add(key)
            if DENIED_PATH.search(name) or mode == '120000' or (not revision and (ROOT / name).is_symlink()):
                errors.append((name, 'excluded path/link'))
                continue
            path = ROOT / name
            if not revision and not path.exists():
                continue
            data = git('cat-file', 'blob', identity) if revision else path.read_bytes()
            if len(data) > 5 * 1024 * 1024:
                errors.append((name, 'oversized public object'))
            if name.endswith(('.png', '.jpg', '.webp')):
                if not name.startswith('docs/images/'):
                    errors.append((name, 'unreviewed image path'))
                continue
            for rule, pattern in RULES.items():
                if pattern.search(data):
                    errors.append((name, rule))
            count += 1
    for name, rule in errors:
        print(f'{name}: {rule}')
    if errors:
        raise SystemExit(1)
    print(f'Public tree gate: PASS ({count} text objects, {len(revisions)} revision(s)); manual review still required')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--commit')
    audit(parser.parse_args().commit)
