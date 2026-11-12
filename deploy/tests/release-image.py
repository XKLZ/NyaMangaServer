#!/usr/bin/env python3
"""Test one exact local AMD64 image using two fresh anonymous Compose projects.

Run as root on an isolated Linux Docker test host. Only resources created here
are removed; evidence is private and must not be uploaded as release assets.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('prepare_beta', ROOT / 'deploy/prepare-beta.py')
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--commit', required=True)
    args = parser.parse_args()
    if os.geteuid() != 0 or sys.platform != 'linux':
        parser.error('isolated Linux Docker test host with root required')
    os.umask(0o077)
    root = args.directory.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    info = json.loads(subprocess.check_output(['docker', 'image', 'inspect', args.image]))[0]
    assert info['Architecture'] == 'amd64' and info['Config']['User'] == '65532:65532'
    assert info['Config']['Labels']['org.opencontainers.image.revision'] == args.commit
    assert info['Config']['Labels']['org.opencontainers.image.version'] == (ROOT / 'internal/buildinfo/VERSION').read_text().strip()
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        port = probe.getsockname()[1]
    origin = f'http://127.0.0.1:{port}'
    projects = []
    evidence = root / 'evidence'

    def run(script, *extra):
        subprocess.run([sys.executable, str(ROOT / 'deploy/tests' / script), *map(str, extra)], check=True)

    try:
        for index in range(2):
            project = 'nyamanga-release-' + uuid.uuid4().hex[:12]
            assert not subprocess.check_output(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + project]).strip()
            path = root / str(index)
            fixtures.prepare(path, origin)
            for name in ('server.json', 'setup-token'):
                os.chown(path / name, 65532, 65532)
                (path / name).chmod(0o400)
            # Loopback only; test instances are never exposed on the host LAN.
            compose = (ROOT / 'deploy/compose.beta.yaml').read_text().replace('"${NYAMANGA_PORT:-19400}:9400"', '"127.0.0.1:${NYAMANGA_PORT}:9400"')
            (path / 'compose.yaml').write_text(compose)
            environment = dict(os.environ, NYAMANGA_IMAGE=args.image, NYAMANGA_BETA_DIR=str(path), NYAMANGA_PORT=str(port))
            command = ['docker', 'compose', '-p', project, '-f', str(path / 'compose.yaml')]
            projects.append((command, environment))
            subprocess.run(command + ['create', 'server'], env=environment, check=True)
            code = """import pathlib,shutil,os,hashlib
root=pathlib.Path('/library');assert not list(root.iterdir())
for p in pathlib.Path('/fixtures').iterdir():
 assert p.is_file() and not p.is_symlink()
 target=root/p.name;shutil.copy2(p,target);os.chown(target,65532,65532);os.chmod(target,0o600)
 assert target.stat().st_mtime_ns==p.stat().st_mtime_ns
 with p.open('rb') as source,target.open('rb') as copied:
  assert hashlib.file_digest(source,'sha256').digest()==hashlib.file_digest(copied,'sha256').digest()
os.chown(root,65532,65532);os.chmod(root,0o700)
"""
            # Restore the same source files, not newly generated ZIPs with new
            # entry timestamps. Keep separate read-only named volumes while
            # preserving both bytes and stat identity used by this exact check.
            source_fixtures = root / '0/fixtures'
            subprocess.run(['docker', 'run', '--rm', '--pull=never', '--network=none', '--user=0:0', '--entrypoint=/usr/local/bin/python', '-v', project + '_anonymous-library:/library', '-v', str(source_fixtures) + ':/fixtures:ro', args.image, '-c', code], check=True)
            subprocess.run(command + ['up', '-d', '--pull', 'never', '--wait', '--wait-timeout', '120'], env=environment, check=True)
            if index == 0:
                run('beta-api.py', '--url', origin, '--token-file', path / 'setup-token', '--evidence', evidence, '--image', path / 'foreground.png')
                run('beta-client.py', '--url', origin, '--evidence', evidence)
                run('beta-recovery.py', 'backup', '--url', origin, '--evidence', evidence)
            else:
                run('beta-recovery.py', 'prepare', '--url', origin, '--evidence', evidence, '--target-token', path / 'setup-token')
                subprocess.run(command + ['stop', 'server'], env=environment, check=True)
                subprocess.run(command + ['up', '-d', '--pull', 'never', '--wait', '--wait-timeout', '120'], env=environment, check=True)
                run('beta-recovery.py', 'verify', '--url', origin, '--evidence', evidence)
                run('beta-recovery.py', 'verify-write-barrier', '--url', origin, '--evidence', evidence)
                run('beta-client.py', '--url', origin, '--evidence', evidence, '--restored')
            subprocess.run(command + ['stop', 'server'], env=environment, check=True)
        print('PASS exact AMD64 image, anonymous API, isolated standard restore and write barrier')
    finally:
        for command, environment in reversed(projects):
            subprocess.run(command + ['down', '--volumes'], env=environment, check=True)


if __name__ == '__main__':
    main()
