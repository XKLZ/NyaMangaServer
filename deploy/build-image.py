#!/usr/bin/env python3
"""Build a paired image from a verified clean Git export, never the working tree."""
import argparse
import json
import hashlib
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def export_source(root, destination):
    identity = subprocess.check_output(['python3', str(root / 'scripts/check/build-identity.py')], text=True).strip()
    if not re.fullmatch(r'[0-9a-f]{40}', identity):
        raise ValueError('container delivery requires clean committed source')
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    archive = destination / 'source.tar'
    subprocess.run(['git', 'archive', '--format=tar', '-o', str(archive), identity], cwd=root, check=True)
    archive.chmod(0o600)
    receipt = {'format': 1, 'commit': identity, 'sha256': digest(archive)}
    receipt_path = destination / 'source.json'
    receipt_path.write_text(json.dumps(receipt, indent=2) + '\n')
    receipt_path.chmod(0o600)
    return receipt


def unpack_source(archive, receipt_path, destination):
    receipt = json.loads(receipt_path.read_text())
    if receipt.get('format') != 1 or not re.fullmatch(r'[0-9a-f]{40}', receipt.get('commit', '')):
        raise ValueError('invalid source receipt')
    if digest(archive) != receipt.get('sha256'):
        raise ValueError('source archive digest mismatch')
    with tarfile.open(archive) as bundle:
        if bundle.pax_headers.get('comment') != receipt['commit']:
            raise ValueError('Git archive commit disagrees with receipt')
        names = set()
        for member in bundle.getmembers():
            parts = Path(member.name).parts
            if member.name in names or not parts or Path(member.name).is_absolute() or '..' in parts or parts[0] in ('.git', '.runtime'):
                raise ValueError('unsafe or duplicate source path')
            if not (member.isfile() or member.isdir()):
                raise ValueError('source archive links and special files are forbidden')
            names.add(member.name)
        bundle.extractall(destination, filter='data')
    return receipt['commit']


def runtime_image(root):
    report = json.loads((root / 'runtime/recommendations/linux-runtime-verification.json').read_text())
    if digest(root / 'runtime/recommendations/requirements-lock-macos-arm64.txt') != report['source_lock_sha256']:
        raise ValueError('dependency closure changed after verification')
    if any(report['platforms'][arch]['status'] != 'verified' for arch in ('amd64', 'arm64')):
        raise ValueError('Linux wheel verification incomplete')
    for arch in ('amd64', 'arm64'):
        rows = (root / f'runtime/recommendations/requirements-lock-linux-{arch}.txt').read_text().splitlines()
        actual = {}
        for row in rows:
            if not row or row.startswith('#'):
                continue
            match = re.fullmatch(r'(\S+==\S+) --hash=sha256:([0-9a-f]{64})', row)
            if not match or match[1] in actual:
                raise ValueError('invalid or duplicated Linux pin')
            actual[match[1]] = match[2]
        expected = {wheel['package']: wheel['sha256'] for wheel in report['platforms'][arch]['wheels']}
        if actual != expected or report['platforms'][arch]['blockers']:
            raise ValueError('Linux lock disagrees with verified wheels')
    image = report['python_image'].get('image', '')
    if report['python_image']['status'] != 'verified' or not re.fullmatch(r'python:3\.12\.0-slim-bookworm@sha256:[0-9a-f]{64}', image):
        raise ValueError('Python image digest unverified')
    return image


def proxy_bypass(environment, hosts):
    if not all(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]*', host) for host in hosts.split(',')):
        raise ValueError('build-no-proxy requires exact host names')
    existing = environment.get('NO_PROXY', '') + ',' + environment.get('no_proxy', '')
    return ','.join(dict.fromkeys(host for host in (existing + ',' + hosts).split(',') if host))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--export-source', type=Path, help='new private export directory; no Docker')
    parser.add_argument('--source-archive', type=Path)
    parser.add_argument('--source-receipt', type=Path)
    parser.add_argument('--platform', choices=('linux/amd64', 'linux/arm64'), default='linux/amd64')
    parser.add_argument('--builder', help='explicit Buildx builder; does not change default')
    parser.add_argument('--build-proxy', help='explicit build-only HTTP(S) proxy, never persisted in image')
    parser.add_argument('--build-no-proxy', help='comma-separated exact hosts bypassing the build proxy')
    parser.add_argument('--go-proxy', help='explicit build-only module proxy; go.sum verification remains enabled')
    parser.add_argument('-t', '--tag')
    parser.add_argument('--output', type=Path, help='new Docker archive; otherwise load into host Docker')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if args.export_source:
        print(json.dumps(export_source(root, args.export_source.resolve())))
        return
    if not args.tag or ':' not in args.tag.rsplit('/', 1)[-1] or args.tag.endswith(':latest') or '@' in args.tag:
        parser.error('a unique non-latest --tag is required')
    if bool(args.source_archive) != bool(args.source_receipt):
        parser.error('--source-archive and --source-receipt must be supplied together')
    if args.output and (args.output.exists() or args.output.with_suffix(args.output.suffix + '.json').exists()):
        parser.error('output or delivery receipt already exists; never overwrite a delivery')
    with tempfile.TemporaryDirectory(prefix='nyamanga-build-') as temporary:
        stage = Path(temporary)
        if not args.source_archive:
            export_source(root, stage / 'export')
        archive = args.source_archive or stage / 'export/source.tar'
        receipt = args.source_receipt or stage / 'export/source.json'
        source = stage / 'source'
        source.mkdir()
        identity = unpack_source(archive, receipt, source)
        version = subprocess.check_output(['python3', str(source / 'scripts/check/release-version.py')], text=True).strip()
        image = runtime_image(source)
        command = ['docker', 'buildx', 'build', '--platform', args.platform, '--progress', 'plain',
                   '--build-arg', 'PYTHON_IMAGE=' + image, '--build-arg', 'COMMIT=' + identity,
                   '--label', 'org.opencontainers.image.revision=' + identity,
                   '--label', 'org.opencontainers.image.version=' + version,
                   '--label', 'org.opencontainers.image.source=https://github.com/XKLZ/NyaMangaServer',
                   '--label', 'org.opencontainers.image.licenses=MIT',
                   '-f', str(source / 'deploy/Dockerfile'), '-t', args.tag]
        if args.builder:
            command += ['--builder', args.builder]
        if args.go_proxy:
            from urllib.parse import urlsplit
            module_proxy = urlsplit(args.go_proxy)
            if module_proxy.scheme not in ('http', 'https') or not module_proxy.hostname or module_proxy.username or module_proxy.password or module_proxy.query or module_proxy.fragment:
                raise ValueError('go-proxy must be one explicit HTTP(S) URL without credentials')
            command += ['--build-arg', 'GOPROXY=' + args.go_proxy]
        environment = os.environ.copy()
        if args.build_proxy:
            from urllib.parse import urlsplit
            proxy = urlsplit(args.build_proxy)
            if proxy.scheme not in ('http', 'https') or not proxy.hostname or proxy.username or proxy.password:
                raise ValueError('build proxy must be an HTTP(S) address without credentials')
            command += ['--build-arg', 'HTTP_PROXY=' + args.build_proxy, '--build-arg', 'HTTPS_PROXY=' + args.build_proxy]
            # Buildx retrieves registry credentials in the client process too.
            environment.update(HTTP_PROXY=args.build_proxy, HTTPS_PROXY=args.build_proxy,
                               http_proxy=args.build_proxy, https_proxy=args.build_proxy)
        if args.build_no_proxy:
            if not args.build_proxy:
                raise ValueError('build-no-proxy requires a proxy and exact host names')
            bypass = proxy_bypass(environment, args.build_no_proxy)
            command += ['--build-arg', 'NO_PROXY=' + bypass, '--build-arg', 'no_proxy=' + bypass]
            environment.update(NO_PROXY=bypass, no_proxy=bypass)
        command += ['--output', 'type=docker,dest=' + str(args.output.resolve())] if args.output else ['--load']
        subprocess.run(command + [str(source)], check=True, env=environment)
        result = {'version': version, 'commit': identity, 'source_sha256': digest(archive), 'tag': args.tag, 'platform': args.platform}
        if args.output:
            args.output.chmod(0o600)
            result['image_archive_sha256'] = digest(args.output)
            result_path = args.output.with_suffix(args.output.suffix + '.json')
            result_path.write_text(json.dumps(result, indent=2) + '\n')
            result_path.chmod(0o600)
        print(json.dumps(result))


if __name__ == '__main__':
    main()
