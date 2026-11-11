#!/usr/bin/env python3
"""One-time, resumable offline promotion. No schema changes or service control."""
import argparse
import base64
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import socket
import sqlite3
import sys

SOURCE = Path('.runtime/tests/runs/work-recommendation-pilot/instance')
TARGET = Path('.runtime/dev')
RECORD = Path('.runtime/maintenance/promote-pilot')
ARCHIVE = Path('.runtime/history/dev-before-promotion-20261022')
PATH_FIELDS = ('data_dir', 'setup_token_file', 'provider_key_file', 'provider_credentials_file')


def safe(p):
    for v in (p, *p.parents):
        if v.is_symlink():
            raise RuntimeError('managed path contains a symlink')


def save(p, value):
    safe(p)
    p.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = p.with_name(p.name + '.tmp')
    with open(temporary, 'w', opener=lambda p, f: os.open(p, f, 0o600)) as f:
        json.dump(value, f, indent=2, ensure_ascii=False)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())
    os.replace(temporary, p)
    syncdir(p.parent)


def syncdir(p):
    fd = os.open(p, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def identity(p):
    safe(p)
    return [p.stat().st_dev, p.stat().st_ino] if p.exists() else None


def digest(b):
    return hashlib.sha256(b).hexdigest()


def resolve(base, value):
    return Path(os.path.abspath(base / value))


def relocate(config, old, new, public):
    config = json.loads(json.dumps(config))
    def move(value):
        if not value:
            return value
        absolute = resolve(old, value)
        try:
            return str(new / absolute.relative_to(old))
        except ValueError:
            return str(absolute)
    for field in PATH_FIELDS:
        if field in config:
            config[field] = move(config[field])
    for field in ('python', 'worker', 'models_dir'):
        if field in config.get('work_recommendations', {}):
            config['work_recommendations'][field] = move(config['work_recommendations'][field])
    for root in config.get('library_roots', []):
        root['path'] = move(root['path'])
    config['listen_addr'] = '127.0.0.1:8080'
    config['public_url'] = public
    return config


def audit(instance):
    c = json.loads((instance / 'config.json').read_text())
    data = resolve(instance, c['data_dir'])
    dbfile = data / 'app.sqlite'
    safe(dbfile)
    if not dbfile.is_file():
        raise RuntimeError('existing database required; never initialize an empty instance')
    db = sqlite3.connect(dbfile.as_uri() + '?mode=ro', uri=True)
    try:
        db.execute('BEGIN')
        tables = [r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")]
        result = {'tables': {}, 'quick_check': db.execute('PRAGMA quick_check').fetchone()[0],
                  'foreign_key_errors': len(db.execute('PRAGMA foreign_key_check').fetchall())}
        if result['quick_check'] != 'ok' or result['foreign_key_errors']:
            raise RuntimeError('database integrity check failed')
        active = db.execute("SELECT count(*) FROM jobs WHERE state IN ('queued','running','paused')").fetchone()[0]
        if active:
            raise RuntimeError('queued, running or paused tasks exist; no task will be cancelled')
        for table in ('work_discovery_runs', 'series_discovery_runs'):
            if table in tables and db.execute('SELECT count(*) FROM ' + table + ' WHERE refresh_required<>0').fetchone()[0]:
                raise RuntimeError('pending background revalidation exists; keep migration paused')
        for table in tables:
            rows = []
            for row in db.execute('SELECT * FROM "' + table.replace('"', '""') + '"'):
                rows.append(json.dumps([{'blob': base64.b64encode(v).decode()} if isinstance(v, bytes) else v for v in row], ensure_ascii=True, separators=(',', ':')))
            rows.sort()
            result['tables'][table] = {'count': len(rows), 'sha256': digest('\n'.join(rows).encode())}
        result['asset_references'] = {'checked': 0, 'missing': 0, 'invalid': 0}
        if 'assets' in tables:
            for key, expected, size in db.execute('SELECT storage_key,digest,size FROM assets'):
                asset = data / key
                safe(asset)
                if not asset.is_relative_to(data) or '..' in Path(key).parts or Path(key).is_absolute():
                    raise RuntimeError('asset storage key outside instance data')
                result['asset_references']['checked'] += 1
                if not asset.is_file():
                    result['asset_references']['missing'] += 1
                elif asset.stat().st_size != size or digest(asset.read_bytes()) != expected:
                    result['asset_references']['invalid'] += 1
        result['schema'] = db.execute('SELECT max(version) FROM schema_migrations').fetchone()[0]
    finally:
        db.close()
    # Complete instance material manifest, including secrets and upgrade snapshots.
    # SQLite/WAL content is verified transactionally above; configs change deliberately.
    result['files'] = {}
    for p in sorted(instance.rglob('*')):
        rel = p.relative_to(instance).as_posix()
        if p.is_symlink():
            raise RuntimeError('instance material symlink is unsupported')
        if not p.is_file() or rel in ('config.json', 'vite-config.json') or p.name in ('app.lock', 'app.sqlite', 'app.sqlite-wal', 'app.sqlite-shm'):
            continue
        result['files'][rel] = {'bytes': p.stat().st_size, 'mode': p.stat().st_mode & 0o777, 'sha256': digest(p.read_bytes())}
    return result


@contextlib.contextmanager
def locks(instances):
    with contextlib.ExitStack() as stack:
        for instance in instances:
            if not instance.exists():
                continue
            c = json.loads((instance / 'config.json').read_text())
            data = resolve(instance, c['data_dir'])
            # Relocated absolute config may point at TARGET while rollback has moved it.
            if not data.exists() and (instance / 'data').exists():
                data = instance / 'data'
            safe(data)
            if not data.is_dir():
                raise RuntimeError('instance data missing')
            f = stack.enter_context(open(data / 'app.lock', 'a+'))
            fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield


def ports_free():
    for port in (8080, 8081, 5173, 5174):
        with socket.socket() as s:
            s.settimeout(.3)
            if s.connect_ex(('127.0.0.1', port)) == 0:
                raise RuntimeError('stop the project API and Vite normally before migration')


def run(root, rollback=False, failpoint=None, check_ports=True):
    root = root.absolute()
    source, target, record, archive = [root / p for p in (SOURCE, TARGET, RECORD, ARCHIVE)]
    for p in (source, target, record, archive):
        safe(p)
    record.mkdir(mode=0o700, parents=True, exist_ok=True)
    with open(record / 'migration.lock', 'a+') as mutex:
        fcntl.flock(mutex, fcntl.LOCK_EX | fcntl.LOCK_NB)
        statefile = record / 'state.json'
        state = json.loads(statefile.read_text()) if statefile.exists() else None
        if state and state['phase'] == 'complete' and not rollback:
            if identity(target) != state['source_identity'] or source.exists():
                raise RuntimeError('completed destination identity changed')
            return state
        if state and state['phase'] == 'rolled_back':
            raise RuntimeError('rolled back: inspect the preserved record before planning another promotion')
        if check_ports:
            ports_free()
        if not state:
            if rollback:
                raise RuntimeError('no migration to roll back')
            if not source.is_dir() or not target.is_dir() or archive.exists():
                raise RuntimeError('source/old daily instance missing or archive target conflicts')
            config = json.loads((source / 'config.json').read_text())
            if resolve(source, config['data_dir']) != source / 'data':
                raise RuntimeError('promotion requires instance-owned data directory')
            if (source / 'vite-config.json').exists():
                raise RuntimeError('unexpected pilot vite config; review before promotion')
            with locks([source, target]):
                before = audit(source)
                old = audit(target)
                originals = {p.name: base64.b64encode(p.read_bytes()).decode() for p in source.glob('*config.json')}
                state = {'phase': 'prepared', 'source_identity': identity(source), 'old_identity': identity(target),
                         'original_configs': originals, 'before': before, 'old_daily': old}
                save(statefile, state)
        # Filesystem identities reconcile a crash between rename and journal write.
        locations = {tuple(identity(p)): p for p in (source, target, archive) if p.exists()}
        src = locations.get(tuple(state['source_identity']))
        old = locations.get(tuple(state['old_identity']))
        if src not in (source, target) or old not in (target, archive):
            raise RuntimeError('migration directory identity conflict')
        if any(p.exists() and p not in (src, old) for p in (source, target, archive)):
            raise RuntimeError('destination conflict; nothing overwritten')
        with locks([src, old]):
            # Refuse any post-promotion changes on rollback, including newer data.
            if rollback:
                current = audit(src)
                expected = state.get('after', state['before'])
                files = current.pop('files')
                baseline_files = expected['files']
                allowed_logs = {'api.log', 'web.log', 'api.managed.pid', 'web.managed.pid'}
                if (current != {k: v for k, v in expected.items() if k != 'files'}
                        or any(files.get(k) != v for k, v in baseline_files.items())
                        or set(files) - set(baseline_files) - allowed_logs):
                    raise RuntimeError('newer destination data exists; rollback refused')
                if state['phase'] == 'complete':
                    original = json.loads(base64.b64decode(state['original_configs']['config.json']))
                    for name, public in [('config.json', 'http://127.0.0.1:8080'), ('vite-config.json', 'http://127.0.0.1:5173')]:
                        if json.loads((src / name).read_text()) != relocate(original, source, target, public):
                            raise RuntimeError('destination configuration changed; rollback refused')
                if src == target:
                    if source.exists():
                        raise RuntimeError('rollback source conflict')
                    os.rename(target, source)
                    syncdir(target.parent)
                for name, content in state['original_configs'].items():
                    p = source / name
                    with open(p, 'wb') as f:
                        f.write(base64.b64decode(content))
                        f.flush()
                        os.fsync(f.fileno())
                (source / 'vite-config.json').unlink(missing_ok=True)
                if old == archive:
                    if target.exists():
                        raise RuntimeError('rollback daily target conflict')
                    os.rename(archive, target)
                    syncdir(target.parent)
                state['phase'] = 'rolled_back'
                save(statefile, state)
                return state
            # Recheck frozen input immediately before moving, including resumed runs.
            if src == source and audit(source) != state['before']:
                raise RuntimeError('source changed after migration preparation')
            if old == target:
                archive.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                os.rename(target, archive)
                syncdir(target.parent)
                state['phase'] = 'archived'
                save(statefile, state)
            if failpoint == 'archived':
                raise RuntimeError('injected interruption after archive')
            if src == source:
                if target.exists():
                    raise RuntimeError('daily target conflict')
                os.rename(source, target)
                syncdir(target.parent)
                syncdir(source.parent)
                state['phase'] = 'moved'
                save(statefile, state)
            if failpoint == 'moved':
                raise RuntimeError('injected interruption after move')
            config = json.loads(base64.b64decode(state['original_configs']['config.json']))
            save(target / 'config.json', relocate(config, source, target, 'http://127.0.0.1:8080'))
            save(target / 'vite-config.json', relocate(config, source, target, 'http://127.0.0.1:5173'))
            after = audit(target)
            if after != state['before']:
                raise RuntimeError('preservation audit mismatch; keep services stopped and inspect private record')
            if audit(archive) != state['old_daily']:
                raise RuntimeError('archived daily preservation mismatch')
            state.update(phase='complete', after=after)
            save(statefile, state)
            return state


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--repository', type=Path, default=Path(__file__).resolve().parents[2])
    p.add_argument('--rollback', action='store_true')
    p.add_argument('--status', action='store_true')
    a = p.parse_args()
    if a.status:
        f = a.repository / RECORD / 'state.json'
        print(json.loads(f.read_text())['phase'] if f.exists() else 'not_started')
    else:
        state = run(a.repository, rollback=a.rollback)
        print('Instance promotion: ' + state['phase'] + '. Private record: .runtime/maintenance/promote-pilot/state.json')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, sqlite3.Error) as e:
        print('Promotion stopped: ' + str(e), file=sys.stderr)
        sys.exit(1)
