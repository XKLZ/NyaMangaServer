#!/usr/bin/env python3
"""Create NEW private anonymous beta material; never invokes Docker or opens an instance."""
import argparse
import json
import os
from pathlib import Path
import secrets
import struct
import urllib.parse
import zipfile
import zlib


def png(width=80, height=120, color=(80, 130, 190)):
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    rows = b''.join(b'\0' + bytes(color) * width for _ in range(height))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>2I5B', width, height, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b'')


def prepare(directory, origin):
    parsed = urllib.parse.urlsplit(origin)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.path not in ('', '/') or parsed.query or parsed.fragment:
        raise ValueError('explicit HTTP(S) origin without credentials or path required')
    directory = directory.absolute()
    if directory.is_symlink() or directory.exists():
        raise ValueError('target must be a new directory')
    if directory.parent.resolve() != directory.parent:
        raise ValueError('symlink parent forbidden')
    os.umask(0o077)
    directory.mkdir(mode=0o700, parents=False)
    (directory / 'setup-token').write_text(secrets.token_urlsafe(32))
    config = {
        'listen_addr': '0.0.0.0:9400', 'public_url': origin.rstrip('/'), 'data_dir': '/data',
        'setup_token_file': '/run/secrets/setup_token', 'provider_key_file': '/secrets/provider-key',
        'provider_credentials_file': '/secrets/providers.enc', 'image_memory_mib': 512,
        'work_recommendations': {'python': '/opt/runtime/bin/python', 'worker': '/opt/nyamanga/recommendations/worker.py', 'models_dir': '/models'},
        'library_roots': [{'root_key': 'anonymous', 'display_name': '匿名测试漫画', 'path': '/library/comics'}],
    }
    (directory / 'server.json').write_text(json.dumps(config, indent=2) + '\n')
    fixtures = directory / 'fixtures'
    fixtures.mkdir()
    for number in range(1, 7):
        with zipfile.ZipFile(fixtures / f'Anonymous-{number}.cbz', 'w', zipfile.ZIP_DEFLATED) as archive:
            for page in range(1, 4):
                archive.writestr(f'{page:03}.png', png(color=(number * 30, page * 60, 120)))
    (fixtures / 'Broken.cbz').write_bytes(b'anonymous invalid zip')
    (directory / 'foreground.png').write_bytes(png())
    (directory / 'receipt.json').write_text(json.dumps({'kind': 'anonymous-beta-v1', 'archives': 6, 'pages_each': 3, 'broken_archives': 1}, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--origin', required=True)
    args = parser.parse_args()
    prepare(args.directory, args.origin)
    print('Prepared private anonymous material; no service started and no secrets printed.')
