#!/usr/bin/env python3
"""Clean-checkout paired binary acceptance with synthetic data and normal restore.

Starts only the supplied binary on a random loopback port. Never opens dev data.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import urllib.request
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('prepare_beta', ROOT / 'deploy/prepare-beta.py')
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server', type=Path, required=True)
    parser.add_argument('--restore', type=Path, required=True)
    parser.add_argument('--directory', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    root = args.directory.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        port = probe.getsockname()[1]
    origin = f'http://127.0.0.1:{port}'
    fixtures.prepare(root / 'input', origin)
    config_files = []
    for name in ('source', 'target'):
        instance = root / name
        instance.mkdir()
        config = json.loads((root / 'input/server.json').read_text())
        config.update(listen_addr=f'127.0.0.1:{port}', data_dir=str(instance / 'data'),
                      setup_token_file=str(root / 'input/setup-token'),
                      provider_key_file=str(instance / 'provider-key'),
                      provider_credentials_file=str(instance / 'providers.enc'))
        config.pop('work_recommendations')
        config['library_roots'][0]['path'] = str(root / 'input/fixtures')
        path = instance / 'config.json'
        path.write_text(json.dumps(config))
        config_files.append(path)
    evidence = root / 'evidence'
    environment = dict(os.environ, NO_PROXY='127.0.0.1,localhost', no_proxy='127.0.0.1,localhost')
    server = None
    log = None

    def stop():
        nonlocal server, log
        if server is not None:
            server.terminate()
            server.wait(timeout=30)
            server = None
        if log:
            log.close()
            log = None

    def start(config):
        nonlocal server, log
        log = (config.parent / 'server.log').open('ab')
        server = subprocess.Popen([str(args.server.resolve()), '--config', str(config)], env=environment, stdout=log, stderr=log)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(200):
            if server.poll() is not None:
                raise RuntimeError('anonymous server exited; inspect its private log')
            try:
                with opener.open(origin + '/api/v1/health/ready', timeout=1) as response:
                    if response.status == 200:
                        with opener.open(origin + '/admin/settings', timeout=5) as page:
                            html = page.read().decode()
                            assert 'text/html' in page.headers['Content-Type']
                            assert page.headers.get('Content-Security-Policy')
                        asset = re.search(r'<script[^>]+src="([^"]+\.js)"', html)
                        assert asset, 'production HTML must refer to a built script'
                        with opener.open(urllib.parse.urljoin(origin, asset[1]), timeout=5) as script:
                            assert 'javascript' in script.headers['Content-Type'] and len(script.read()) > 1000
                        return
            except OSError:
                pass
            time.sleep(.1)
        raise RuntimeError('anonymous server readiness timed out')

    def run(script, *extra):
        subprocess.run([sys.executable, str(ROOT / 'deploy/tests' / script), *map(str, extra)], env=environment, check=True)

    try:
        start(config_files[0])
        run('beta-api.py', '--url', origin, '--token-file', root / 'input/setup-token', '--evidence', evidence, '--image', root / 'input/foreground.png')
        run('beta-recovery.py', 'backup', '--url', origin, '--evidence', evidence)
        stop()
        start(config_files[1])
        run('beta-recovery.py', 'prepare', '--url', origin, '--evidence', evidence, '--target-token', root / 'input/setup-token')
        stop()
        start(config_files[1])
        run('beta-recovery.py', 'verify', '--url', origin, '--evidence', evidence)
        run('beta-recovery.py', 'verify-write-barrier', '--url', origin, '--evidence', evidence)
        stop()
        status = subprocess.check_output([str(args.restore.resolve()), '--config', str(config_files[1]), '--status'], env=environment)
        json.loads(status)
        (root / 'paired-restore-status.json').write_bytes(status)
        (root / 'summary.json').write_text(json.dumps({'status': 'passed', 'scope': 'native synthetic API and standard restore', 'real_model': False, 'docker': False}) + '\n')
        print('PASS anonymous production assets, API and standard restore; both instances stopped')
    finally:
        stop()


if __name__ == '__main__':
    main()
