#!/usr/bin/env python3
"""Prepare only the explicitly isolated beta project; run with NAS sudo.

No existing destination, project volume or container is accepted. Image must
already be loaded and its revision label must match the delivery receipt.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys


def docker(*args):
    return subprocess.check_output(['docker', *args], text=True)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--delivery', type=Path, required=True)
    p.add_argument('--directory', type=Path, required=True)
    p.add_argument('--project', default='nyamangaserver-beta', choices=['nyamangaserver-beta', 'nyamangaserver-beta-restore'])
    p.add_argument('--origin', required=True)
    p.add_argument('--image', required=True)
    p.add_argument('--commit', required=True)
    args = p.parse_args()
    if os.geteuid() != 0:
        p.error('run with sudo for only this new project directory and volumes')
    expected = args.directory.absolute()
    if expected.parent.resolve() != expected.parent or expected.name != args.project or expected.exists() or expected.is_symlink():
        p.error('new fixed beta directory required; refusing overwrite')
    for kind in ['container', 'volume', 'network']:
        listing = ['ls', '-a', '-q'] if kind == 'container' else ['ls', '-q']
        if docker(kind, *listing, '--filter', 'label=com.docker.compose.project=' + args.project).strip():
            p.error('project already has resources; inspect instead of initializing again')
    info = json.loads(docker('image', 'inspect', args.image))[0]
    assert info['Architecture'] == 'amd64'
    assert info['Config']['Labels']['org.opencontainers.image.revision'] == args.commit
    assert info['Config']['User'] == '65532:65532'
    source = args.delivery.resolve()
    subprocess.run([sys.executable, str(source / 'prepare-beta.py'), '--directory', str(expected), '--origin', args.origin], check=True)
    os.umask(0o077)
    shutil.copyfile(source / 'compose.beta.yaml', expected / 'compose.yaml')
    for filename in ['beta-provider.py', 'compose.beta-fixture.yaml']:
        shutil.copyfile(source / filename, expected / filename)
    (expected / '.env').write_text(f'NYAMANGA_IMAGE={args.image}\nNYAMANGA_BETA_DIR={expected}\nNYAMANGA_PORT=19400\nCOMPOSE_PROJECT_NAME={args.project}\n')
    for root, directories, files in os.walk(expected):
        os.chown(root, 65532, 65532)
        os.chmod(root, 0o700)
        for filename in files:
            path = Path(root) / filename
            os.chown(path, 65532, 65532)
            os.chmod(path, 0o600)
    compose = ['docker', 'compose', '--project-directory', str(expected), '-p', args.project, '-f', str(expected / 'compose.yaml')]
    with socket.socket() as probe:
        probe.bind(('0.0.0.0', 19400))
    subprocess.run(compose + ['create', 'server'], check=True)
    volume = args.project + '_anonymous-library'
    code = """import os,pathlib,shutil
root=pathlib.Path('/library'); assert not list(root.iterdir()), 'refuse existing anonymous volume'
for p in pathlib.Path('/fixtures').iterdir():
 assert p.is_file() and not p.is_symlink(); target=root/p.name;shutil.copyfile(p,target);os.chown(target,65532,65532);os.chmod(target,0o600)
os.chown(root,65532,65532);os.chmod(root,0o700)
"""
    subprocess.run(['docker', 'run', '--rm', '--pull=never', '--network', 'none', '--user', '0:0', '--entrypoint', '/usr/local/bin/python', '-v', volume + ':/library', '-v', str(expected / 'fixtures') + ':/fixtures:ro', args.image, '-c', code], check=True)
    print('Prepared isolated project and seven anonymous archives; application is not started.')


if __name__ == '__main__':
    main()
