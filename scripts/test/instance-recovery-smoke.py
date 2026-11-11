#!/usr/bin/env python3
"""Real anonymous fixture processes, authenticated ZIP transfer and stopped CLI.

No production web build, real models, remote providers or daily instance access.
"""
import http.cookiejar
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
TESTS = ROOT / '.runtime/tests'
TESTS.mkdir(parents=True, exist_ok=True)
RUN = Path(tempfile.mkdtemp(prefix='instance-recovery-smoke-', dir=TESTS))
ENV = {**os.environ, 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off',
       'GOPATH': str(ROOT / '.runtime/cache/go'), 'GOCACHE': str(ROOT / '.runtime/cache/go-build'),
       'TMPDIR': str(ROOT / '.runtime/tests/tmp')}
BASE = 'http://127.0.0.1:18082'
jar = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar))
csrf = ''


def command(*args):
    return subprocess.check_output(args, cwd=ROOT, env=ENV, text=True).strip()


def call(method, path, data=None, raw=False, key=None, expected=200):
    global csrf
    body = data if isinstance(data, bytes) else json.dumps(data).encode() if data is not None else None
    headers = {'Origin': BASE, 'X-CSRF-Token': csrf}
    if body is not None:
        headers['Content-Type'] = 'application/zip' if isinstance(data, bytes) else 'application/json'
    if key:
        headers['Idempotency-Key'] = key
    request = urllib.request.Request(BASE + '/api/v1' + path, data=body, method=method, headers=headers)
    try:
        response = client.open(request, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    payload = response.read()
    assert response.code == expected, (method, path, response.code, payload[:300])
    if raw:
        return payload
    result = json.loads(payload)
    if isinstance(result, dict) and 'csrf_token' in result:
        csrf = result['csrf_token']
    return result


def wait_operation(kind, identity, state):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        detail = call('GET', f'/maintenance/{kind}/{identity}')
        operation = detail['operation'] if 'operation' in detail else detail
        if operation['state'] == state:
            return detail
        assert operation['state'] not in ('failed', 'cancelled'), operation
        time.sleep(.05)
    raise AssertionError('operation deadline')


def start(config, executable):
    process = subprocess.Popen([str(executable), '--serve', str(config)], cwd=ROOT, env=ENV,
                               stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    for _ in range(100):
        assert process.poll() is None, process.stderr.read().decode()[:300]
        try:
            call('GET', '/health/ready')
            return process
        except (OSError, AssertionError):
            time.sleep(.05)
    raise AssertionError('fixture did not start')


def stop(process):
    process.send_signal(signal.SIGTERM)
    assert process.wait(timeout=15) == 0, process.stderr.read().decode()[:300]
    process.stderr.close()


def main():
    executable = RUN / 'fixtures'
    offline = RUN / 'instance-restore'
    command('go', 'build', '-trimpath', '-o', str(executable), './cmd/fixtures')
    command('go', 'build', '-trimpath', '-ldflags', '-X main.version=anonymous-m3-test -X main.commit=anonymous-fixture', '-o', str(offline), './cmd/instance-restore')
    config = Path(command(str(executable), '--recovery-instance', str(RUN / 'instance')))
    values = json.loads(config.read_text()); values['listen_addr'] = '127.0.0.1:18082'; values['public_url'] = BASE
    config.write_text(json.dumps(values)); config.chmod(0o600)
    process = start(config, executable)
    try:
        call('POST', '/auth/login', {'password': 'anonymous recovery password'})
        backup = call('POST', '/maintenance/backups', {'request_key': 'anonymous-process-backup'}, expected=202)
        wait_operation('backups', backup['id'], 'completed')
        archive = call('GET', f"/maintenance/backups/{backup['id']}/download", raw=True)
        uploaded = call('POST', '/maintenance/restores', archive, key='anonymous-process-restore', expected=201)
        identity = uploaded['id']
        call('POST', f'/maintenance/restores/{identity}/validate', {'expected_revision': uploaded['revision']}, expected=202)
        validated = wait_operation('restores', identity, 'validated')
        assert validated['summary']['object_counts']['work_proposal_decisions'] > 0
        preview = call('POST', f'/maintenance/restores/{identity}/preview', {
            'expected_revision': validated['operation']['revision'], 'acknowledge_stat_only': True,
            'root_mappings': [{'library_id': 'fixture-library', 'target_root_key': None}]})
        call('POST', f'/maintenance/restores/{identity}/confirm', {
            'expected_revision': preview['operation']['revision'], 'request_key': 'anonymous-process-confirm'}, expected=202)
    finally:
        stop(process)
    process = start(config, executable)
    try:
        call('GET', '/auth/session', expected=401)
        call('POST', '/auth/login', {'password': 'anonymous recovery password'})
        state = call('GET', '/maintenance/instance-state')
        assert state['background_hold'] and state['write_epoch'] == 0
        time.sleep(1.3)  # allow every 1s orchestration loop to tick at least once
        after = call('GET', '/maintenance/instance-state')
        assert after['write_epoch'] == 0, after
        activation = call('GET', '/maintenance/instance-activation')
    finally:
        stop(process)
    db = sqlite3.connect(f"file:{values['data_dir']}/app.sqlite?mode=ro", uri=True)
    assert db.execute("SELECT count(*) FROM jobs WHERE state IN('queued','running')").fetchone()[0] == 0
    assert db.execute("SELECT state FROM import_batches WHERE id='fixture-import'").fetchone()[0] == 'review'
    assert db.execute("SELECT next_scan_at FROM libraries WHERE id='fixture-library'").fetchone()[0] == 1
    db.close()
    command(str(offline), '--config', str(config), '--rollback', str(activation['revision']))
    process = start(config, executable)
    try:
        call('POST', '/auth/login', {'password': 'anonymous recovery password'})
        result = call('GET', '/maintenance/instance-activation')
        assert result['state'] == 'rolled_back'
        assert call('GET', '/libraries')['items'][0]['root_key'] == 'fixture'
    finally:
        stop(process)
    report = {'passed': True, 'schema': 24, 'authenticated_zip_roundtrip': True,
              'normal_restart_activation': True, 'old_session_invalidated': True,
              'normal_login_checkpoint_rollback': True, 'automatic_work_epoch_after_ticks': 0,
              'production_web_built': False, 'real_models_or_network': False, 'docker_executed': False}
    (RUN / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))


if __name__ == '__main__':
    main()
