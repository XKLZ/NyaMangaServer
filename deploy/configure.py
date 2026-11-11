#!/usr/bin/env python3
"""Prepare a new private Docker configuration, without starting Docker or a database."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parent


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def safe_value(value):
    if any(c in value for c in ('\n', '\r', '\0', "'", '$')):
        raise ValueError('configuration values may not contain quotes, dollars or line breaks')
    return value


def prepare(directory, origin, port=9400, library=None, owner=(65532, 65532)):
    origin = safe_value(origin).rstrip('/')
    parsed = urlsplit(origin)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment:
        raise ValueError('origin must be the browser HTTP(S) origin, without path or credentials')
    parsed.port  # Validate port syntax and range.
    if not 1 <= port <= 65535:
        raise ValueError('host port out of range')
    directory = Path(os.path.abspath(directory))
    safe_value(str(directory))
    if directory.is_symlink() or directory.parent.resolve() != directory.parent or not directory.parent.is_dir():
        raise ValueError('use an existing real parent directory; symlink paths are forbidden')
    if library is not None:
        library = Path(os.path.abspath(library))
        safe_value(str(library))
        if library.resolve() != library or not library.is_dir():
            raise ValueError('library must be an existing real directory')
        if directory == library or directory.is_relative_to(library) or library.is_relative_to(directory):
            raise ValueError('configuration and source library must be separate')
    options = dict(origin=origin, port=port, library=str(library) if library else None, owner=list(owner))
    receipt_path = directory / 'installation.json'
    if directory.exists():
        if not receipt_path.is_file() or receipt_path.is_symlink():
            raise ValueError('existing directory without a complete receipt; no files changed')
        receipt = json.loads(receipt_path.read_text())
        if receipt.get('format') != 1 or receipt.get('options') != options:
            raise ValueError('existing configuration differs; no files changed')
        expected_files = {'compose.yaml', '.env', 'server.json', 'setup-token'}
        if library:
            expected_files.add('compose.override.yaml')
        if set(receipt.get('sha256', {})) != expected_files or directory.stat().st_mode & 0o777 != 0o700:
            raise ValueError('incomplete receipt or changed directory permissions')
        for name, digest in receipt['sha256'].items():
            if name not in ('compose.yaml', 'compose.override.yaml', '.env', 'server.json', 'setup-token'):
                raise ValueError('invalid installation receipt')
            path = directory / name
            if path.is_symlink() or not path.is_file() or sha(path) != digest:
                raise ValueError('incomplete or modified installation; no files changed')
            expected_mode = 0o400 if name in ('server.json', 'setup-token') else 0o600
            if path.stat().st_mode & 0o777 != expected_mode:
                raise ValueError('changed installation permissions; no files changed')
            if name in ('server.json', 'setup-token') and (path.stat().st_uid, path.stat().st_gid) != owner:
                raise ValueError('changed container file ownership; no files changed')
        return 'Existing matching configuration preserved; no database or service created.'
    # Reserve the destination exclusively. An interrupted partial install is never
    # silently replaced or completed with a different initialization secret.
    os.umask(0o077)
    directory.mkdir(mode=0o700)
    # Let the invoking user enter the private install directory after sudo. The
    # daemon mounts the two UID65532 files directly; no world-readable secret.
    os.chown(directory, int(os.environ.get('SUDO_UID', os.getuid())),
             int(os.environ.get('SUDO_GID', os.getgid())))
    config = json.loads((ROOT / 'config.example.json').read_text())
    config['public_url'] = origin
    if library:
        config['library_roots'] = [{'root_key': 'comics', 'display_name': '漫画目录', 'path': '/library/comics'}]
    (directory / 'server.json').write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n')
    (directory / 'setup-token').write_text(secrets.token_urlsafe(32) + '\n')
    shutil.copyfile(ROOT / 'compose.release.yaml', directory / 'compose.yaml')
    values = {'COMPOSE_PROJECT_NAME': 'nyamangaserver', 'NYAMANGA_CONFIG_DIR': str(directory), 'NYAMANGA_PORT': str(port)}
    if library:
        values['NYAMANGA_LIBRARY_DIR'] = str(library)
        shutil.copyfile(ROOT / 'compose.library.yaml', directory / 'compose.override.yaml')
    (directory / '.env').write_text(''.join(f"{key}='{value}'\n" for key, value in values.items()))
    hashes = {}
    for path in directory.iterdir():
        path.chmod(0o600)
        if path.name in ('server.json', 'setup-token'):
            os.chown(path, *owner)
            path.chmod(0o400)
        hashes[path.name] = sha(path)
    receipt_path.write_text(json.dumps({'format': 1, 'options': options, 'sha256': hashes}, indent=2) + '\n')
    receipt_path.chmod(0o600)
    return 'Private configuration prepared. No service started; token was not printed.'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--origin', required=True)
    parser.add_argument('--port', type=int, default=9400)
    parser.add_argument('--library', type=Path)
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('run with sudo on the Docker host to set the two mounted files to UID 65532; Docker is not invoked')
    try:
        print(prepare(args.directory, args.origin, args.port, args.library))
    except (ValueError, OSError) as error:
        parser.exit(1, f'Preparation stopped: {error}\n')
