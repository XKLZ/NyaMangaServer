#!/usr/bin/env python3
"""One-off, manifest-bound permanent removal of retired development data.

Never starts an application, follows symlinks, or resets a database. The CLI
only operates in this repository; tests exercise the engine in temporary roots.
"""
import argparse
from contextlib import contextmanager, ExitStack
import fcntl
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess

ROOT = Path(__file__).resolve().parents[2]
RECORD = '.runtime/maintenance/restore-preparation-20261104'
TREES = (
    '.runtime/dev', '.runtime/history', '.runtime/tests',
    '.runtime/cache/go-build', '.runtime/cache/vite-dev',
    '.runtime/cache/vite-instance-ui', '.runtime/cache/vite-recommendation-pilot',
    '.runtime/environment/go-cache', '.runtime/environment/smoke',
)
OLD_BINARIES = (
    'compatibility-ui-fixtures', 'compatibility-ui-server', 'dev-reset',
    'fixtures', 'recommendation-pilot-server', 'recovery-ui-fixtures',
    'server.before-discovery-label-fixes', 'server.before-import-workflow',
    'server.import-workflow',
)


def encode(value):
    return (json.dumps(value, sort_keys=True, ensure_ascii=True) + '\n').encode()


def private_write(path, value):
    temp = path.with_suffix(path.suffix + '.tmp')
    with temp.open('xb') as f:
        os.chmod(temp, 0o600)
        f.write(encode(value))
        f.flush()
        os.fsync(f.fileno())
    os.replace(temp, path)
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def append_record(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'ab') as f:
        f.write(encode(value))
        f.flush()
        os.fsync(f.fileno())
    directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def identity(path):
    s = path.lstat()
    if not (stat.S_ISREG(s.st_mode) or stat.S_ISDIR(s.st_mode)):
        raise RuntimeError('symlink or special file rejected')
    result = [s.st_dev, s.st_ino, s.st_mode]
    # Directory timestamps change as children are removed; identity does not.
    if stat.S_ISREG(s.st_mode):
        result += [s.st_size, s.st_mtime_ns, s.st_ctime_ns]
    return result


def allowed(path):
    p = Path(path)
    if p.is_absolute() or '..' in p.parts or str(p) != path:
        return False
    if path in TREES or path in ('.runtime/.DS_Store', '.runtime/cache/.DS_Store'):
        return True
    if p.parent == Path('.runtime/bin') and p.name in OLD_BINARIES:
        return True
    return p.parent == Path('.runtime/maintenance') and path != RECORD


def candidates(root):
    paths = list(TREES) + ['.runtime/.DS_Store', '.runtime/cache/.DS_Store']
    paths += ['.runtime/bin/' + n for n in OLD_BINARIES]
    maintenance = root / '.runtime/maintenance'
    if maintenance.exists():
        paths += [str(p.relative_to(root)) for p in maintenance.iterdir()
                  if str(p.relative_to(root)) != RECORD]
    return sorted(p for p in paths if os.path.lexists(root / p))


def scan(root, paths):
    entries, parents = {}, {}
    for key in paths:
        if not allowed(key):
            raise RuntimeError('path outside deletion allowlist')
        p = root / key
        for parent in p.parents:
            if parent == root:
                break
            parents[str(parent.relative_to(root))] = identity(parent)
        def visit(item):
            rel = str(item.relative_to(root))
            ident = identity(item)
            entries[rel] = ident
            if stat.S_ISDIR(ident[2]):
                for child in sorted(item.iterdir()):
                    visit(child)
        if os.path.lexists(p):
            visit(p)
    return entries, parents


def preview(root):
    paths = candidates(root)
    entries, parents = scan(root, paths)
    return {'format': 1, 'root': str(root), 'root_identity': identity(root),
            'paths': paths, 'entries': entries, 'parents': parents,
            'file_bytes': sum(v[3] for v in entries.values() if len(v) > 3)}


def validate(root, manifest, intents):
    if manifest['format'] != 1 or manifest['root'] != str(root):
        raise RuntimeError('manifest repository mismatch')
    if identity(root) != manifest['root_identity']:
        raise RuntimeError('repository identity changed')
    for p in manifest['entries']:
        if Path(p).is_absolute() or '..' in Path(p).parts or str(Path(p)) != p:
            raise RuntimeError('invalid entry path')
        if not any(p == base or p.startswith(base + '/') for base in manifest['paths']):
            raise RuntimeError('entry outside frozen roots')
    for p, expected in manifest['parents'].items():
        if identity(root / p) != expected:
            raise RuntimeError('parent identity changed')
    current, _ = scan(root, manifest['paths'])
    expected = manifest['entries']
    if any(p not in expected or expected[p] != ident for p, ident in current.items()):
        raise RuntimeError('manifest stale: new or changed object')
    if any(p not in current and p not in intents for p in expected):
        raise RuntimeError('manifest stale: object disappeared without deletion intent')


@contextmanager
def parent_fd(root, relative, manifest):
    fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    prefix = Path()
    try:
        for part in Path(relative).parts[:-1]:
            prefix /= part
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = child
            s = os.fstat(fd)
            expected = manifest['entries'].get(str(prefix), manifest['parents'].get(str(prefix)))
            if expected is None or [s.st_dev, s.st_ino, s.st_mode] != expected[:3]:
                raise RuntimeError('parent replaced during deletion')
        yield fd
    finally:
        os.close(fd)


def apply(root, manifest, journal, guard, max_batches=None):
    """Persist batch intent before removal; absent objects require that intent.

    max_batches is only an engine test seam, not a CLI option. Recreated paths
    always fail identity checks, even after a completed run.
    """
    digest = hashlib.sha256(encode(manifest)).hexdigest()
    records = [json.loads(line) for line in journal.read_text().splitlines()] if journal.exists() else []
    if records and records[0].get('manifest_sha256') != digest:
        raise RuntimeError('journal belongs to another manifest')
    guard()
    if any(record.get('complete') for record in records):
        if any(os.path.lexists(root / p) for p in manifest['paths']):
            raise RuntimeError('completed plan cannot delete recreated targets')
    intents = {p for record in records for p in record.get('intents', [])}
    validate(root, manifest, intents)
    if not records:
        append_record(journal, {'manifest_sha256': digest})
    entries = manifest['entries']
    paths = sorted(entries, key=lambda p: (-len(Path(p).parts), p))
    remaining = [p for p in paths if os.path.lexists(root / p)]
    for batch_no, offset in enumerate(range(0, len(remaining), 128)):
        guard()
        batch = remaining[offset:offset + 128]
        intents.update(batch)
        append_record(journal, {'intents': batch})
        for p in batch:
            with parent_fd(root, p, manifest) as fd:
                name = Path(p).name
                s = os.stat(name, dir_fd=fd, follow_symlinks=False)
                actual = [s.st_dev, s.st_ino, s.st_mode]
                if stat.S_ISREG(s.st_mode):
                    actual += [s.st_size, s.st_mtime_ns, s.st_ctime_ns]
                if actual != entries[p]:
                    raise RuntimeError('object changed during deletion')
                if stat.S_ISDIR(s.st_mode):
                    os.rmdir(name, dir_fd=fd)
                else:
                    os.unlink(name, dir_fd=fd)
        if max_batches is not None and batch_no + 1 >= max_batches:
            return
    append_record(journal, {'complete': True})


def runtime_idle(root):
    # Fail closed if process inspection is unavailable. Do not print command lines.
    ps = subprocess.run(['ps', '-axo', 'pid=,comm=,args='], capture_output=True, text=True, check=True)
    for line in ps.stdout.splitlines():
        parts = line.split(None, 2)
        if len(parts) != 3 or int(parts[0]) == os.getpid():
            continue
        _, comm, args = parts
        if any(x in Path(comm).name.lower() for x in ('server', 'fixtures', 'node', 'go', 'python')):
            if str(root / '.runtime') in args or str(root / 'web/node_modules/.bin/vite') in args:
                if 'prepare-restore-cleanup.py' not in args:
                    raise RuntimeError('project runtime/test process remains; stop normally first')
    ports = subprocess.run(['lsof', '-nP', '-iTCP:8080', '-iTCP:5173', '-sTCP:LISTEN'], capture_output=True)
    if ports.returncode not in (0, 1) or ports.stderr or ports.stdout:
        raise RuntimeError('standard ports occupied or inspection failed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['preview', 'apply'])
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--permanently-delete-test-data', action='store_true')
    args = parser.parse_args()
    os.umask(0o077)
    record = ROOT / RECORD
    for p in (ROOT / '.runtime', ROOT / '.runtime/maintenance', record):
        if p.is_symlink():
            raise RuntimeError('record directory must not be a symlink')
    record.mkdir(mode=0o700, parents=True, exist_ok=True)
    manifest_path = Path(args.manifest).absolute()
    if manifest_path.parent != record or manifest_path.name != 'manifest.json':
        raise RuntimeError('manifest must be the dedicated private manifest.json')
    # Exclusive cleanup operator lock, independent of the data being deleted.
    with (record / 'cleanup.lock').open('a') as operator:
        fcntl.flock(operator, fcntl.LOCK_EX | fcntl.LOCK_NB)
        runtime_idle(ROOT)
        tracked = subprocess.check_output(['git', 'ls-files', '-z', '.runtime'], cwd=ROOT)
        if tracked:
            raise RuntimeError('tracked runtime files require explicit scope review')
        if args.action == 'preview':
            if manifest_path.exists():
                raise RuntimeError('manifest already exists; never overwrite an execution plan')
            m = preview(ROOT)
            private_write(manifest_path, m)
            print(json.dumps({'paths': m['paths'], 'objects': len(m['entries']), 'bytes': m['file_bytes']}))
        else:
            if not args.permanently_delete_test_data:
                raise RuntimeError('explicit permanent deletion flag required')
            if manifest_path.stat().st_mode & 0o077:
                raise RuntimeError('manifest must be private')
            m = json.loads(manifest_path.read_text())
            if any(not allowed(p) for p in m['paths']):
                raise RuntimeError('manifest path outside deletion allowlist')
            with ExitStack() as locks:
                for p in m['entries']:
                    if Path(p).name == 'app.lock' and os.path.lexists(ROOT / p):
                        fd = os.open(ROOT / p, os.O_RDWR | os.O_NOFOLLOW)
                        locks.callback(os.close, fd)
                        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                apply(ROOT, m, record / 'deletion.jsonl', lambda: runtime_idle(ROOT))
            print('Frozen deletion plan complete; services remain stopped.')


if __name__ == '__main__':
    main()
