#!/usr/bin/env python3
"""Create a NEW ephemeral CA for anonymous production-provider tests only."""
import argparse
import os
from pathlib import Path
import subprocess


def create(directory):
    if directory.exists() or directory.is_symlink():
        raise ValueError('refuse existing fixture CA')
    os.umask(0o077)
    directory.mkdir(mode=0o700)
    def ssl(*args):
        subprocess.run(['openssl', *args], cwd=directory, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    ssl('req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'ca.key', '-out', 'ca.pem', '-days', '2', '-subj', '/CN=NyaManga anonymous beta CA')
    ssl('req', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'server.key', '-out', 'server.csr', '-subj', '/CN=api.e-hentai.org')
    (directory / 'extensions.cnf').write_text('subjectAltName=DNS:api.e-hentai.org,DNS:e-hentai.org,DNS:exhentai.org\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
    ssl('x509', '-req', '-in', 'server.csr', '-CA', 'ca.pem', '-CAkey', 'ca.key', '-CAcreateserial', '-out', 'server.pem', '-days', '2', '-extfile', 'extensions.cnf')
    for path in directory.iterdir():
        path.chmod(0o600)
        if os.geteuid() == 0:
            os.chown(path, 65532, 65532)
    if os.geteuid() == 0:
        os.chown(directory, 65532, 65532)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    create(parser.parse_args().directory)
    print('Created test-only CA; no host trust store changed.')
