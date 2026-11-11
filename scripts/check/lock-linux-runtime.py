#!/usr/bin/env python3
"""Verify official same-version wheels; no installation, source build or models.

Run explicitly with network authorization. Preserve the macOS lock. A partial
resolution writes evidence, never a usable Linux requirements lock.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile
import io
import struct
import zipfile
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[2]
RUNTIME = ROOT / 'runtime/recommendations'
PYTHON_ARCHIVE = 'https://raw.githubusercontent.com/docker-library/repo-info/3dae0ec5c55c6a52a67d7349e72bf6687d27a44d/repos/python/remote/3.12.0-slim-bookworm.md'
PYTHON_ARCHIVE_SHA256 = '2855fe038abe8ec8d3bd4cf5f2debeeb0ac9d5ac8c1f9d4c66a11a43e1e9d187'


def official_python_archive(temp, cached=None):
    raw = Path(cached).read_bytes() if cached else fetch(PYTHON_ARCHIVE, temp / 'python-official-archive')
    if hashlib.sha256(raw).hexdigest() != PYTHON_ARCHIVE_SHA256:
        raise RuntimeError('official_archive_digest_mismatch')
    text = raw.decode()
    if not text.startswith('## `python:3.12.0-slim-bookworm`') or 'ENV PYTHON_VERSION=3.12.0' not in text:
        raise RuntimeError('official_archive_identity_mismatch')
    digest = re.search(r'docker pull python@(sha256:[0-9a-f]{64})', text)[1]
    platforms = {}
    for arch in ('amd64', 'arm64'):
        section = re.search(r'### `python:3\.12\.0-slim-bookworm` - linux; ' + arch + r'[^\n]*\n(.*?)(?=\n### |\Z)', text, re.S)
        if not section:
            raise RuntimeError('official_archive_missing_platform')
        platforms[arch] = re.search(r'docker pull python@(sha256:[0-9a-f]{64})', section[1])[1]
    return {'status': 'verified', 'image': 'python:3.12.0-slim-bookworm@' + digest,
            'source': PYTHON_ARCHIVE, 'source_sha256': PYTHON_ARCHIVE_SHA256,
            'method': 'Docker official repo-info immutable archive; not a current tag lookup or image execution',
            'platforms': platforms, 'image_execution_verified': False}


def elf_needed(raw):
    if not raw.startswith(b'\x7fELF'):
        return []
    if raw[4:6] != b'\x02\x01':
        raise RuntimeError('unsupported_elf_format')
    offset = struct.unpack_from('<Q', raw, 40)[0]
    entry_size, count = struct.unpack_from('<HH', raw, 58)
    if entry_size != 64 or count > 4096 or offset + count * 64 > len(raw):
        raise RuntimeError('invalid_elf_sections')
    sections = [struct.unpack_from('<IIQQQQIIQQ', raw, offset + i * 64) for i in range(count)]
    needed = []
    for section in sections:
        if section[1] != 6:
            continue
        linked = sections[section[6]]
        strings = raw[linked[4]:linked[4] + linked[5]]
        for position in range(section[4], section[4] + section[5], 16):
            tag, value = struct.unpack_from('<qQ', raw, position)
            if tag == 1:
                needed.append(strings[value:].split(b'\0', 1)[0].decode('ascii'))
    return sorted(set(needed))


def wheel_native_dependencies(raw):
    needed, provided = set(), set()
    with zipfile.ZipFile(io.BytesIO(raw)) as wheel:
        for entry in wheel.infolist():
            if '.so' not in Path(entry.filename).name:
                continue
            if entry.file_size > 128 << 20:
                raise RuntimeError('native_inspection_budget')
            content = wheel.read(entry)
            if content.startswith(b'\x7fELF'):
                provided.add(Path(entry.filename).name)
                needed.update(elf_needed(content))
    return {'needed': sorted(needed), 'provided': sorted(provided)}


def check_local():
    report = json.loads((RUNTIME / 'linux-runtime-verification.json').read_text())
    source = (RUNTIME / 'requirements-lock-macos-arm64.txt').read_bytes()
    assert hashlib.sha256(source).hexdigest() == report['source_lock_sha256']
    pins = {line.split()[0] for line in source.decode().splitlines() if line and not line.startswith('#')}
    assert len(pins) == 23
    for arch in ('amd64', 'arm64'):
        result = report['platforms'][arch]
        assert result['status'] == 'verified' and not result['blockers']
        assert {wheel['package'] for wheel in result['wheels']} == pins
        actual_lock = {line.split()[0]: line.split('sha256:')[1] for line in (RUNTIME / f'requirements-lock-linux-{arch}.txt').read_text().splitlines() if line and not line.startswith('#')}
        assert actual_lock == {wheel['package']: wheel['sha256'] for wheel in result['wheels']}
        needed, provided = set(), set()
        for wheel in result['wheels']:
            assert wheel_supported(wheel['filename'], {'amd64': 'x86_64', 'arm64': 'aarch64'}[arch])
            assert re.fullmatch('[0-9a-f]{64}', wheel['sha256']) and wheel['bytes'] > 0
            needed.update(wheel['native_dependencies']['needed'])
            provided.update(wheel['native_dependencies']['provided'])
        base = {'libc.so.6', 'libdl.so.2', 'libm.so.6', 'libpthread.so.0', 'librt.so.1',
                'ld-linux-x86-64.so.2', 'ld-linux-aarch64.so.1'}
        copied = {'libstdc++.so.6', 'libgcc_s.so.1', 'libgomp.so.1', 'libz.so.1'}
        assert not needed - provided - base - copied, needed - provided - base - copied
    image = report['python_image']
    assert image['status'] == 'verified' and {'amd64', 'arm64'} <= set(image['platforms'])
    assert image['source_sha256'] == PYTHON_ARCHIVE_SHA256
    assert image['image'] in (ROOT / 'deploy/Dockerfile').read_text()
    recorded = json.loads((ROOT / 'deploy/base-images.json').read_text())['python']
    assert image['image'].endswith('@' + recorded['index_digest'])
    print('Local fixed-version closures, downloaded wheel records, ELF dependency coverage and official image metadata: PASS (no Docker/model execution).')
    return 0


def fetch(url, target, headers=()):
    args = ['curl', '--noproxy', '*', '--silent', '--show-error', '--fail',
            '--connect-timeout', '15', '--max-time', '90', '--proto', '=https']
    for header in headers:
        args += ['-H', header]
    result = subprocess.run(args + ['-o', str(target), url], capture_output=True)
    if result.returncode:
        raise RuntimeError(f'official_download_failed:curl_{result.returncode}')
    return target.read_bytes()


def wheel_supported(name, arch):
    if not name.endswith('.whl'):
        return False
    tags = name[:-4].rsplit('-', 3)[1:]
    if len(tags) != 3:
        return False
    python, abi, platforms = tags
    interpreters = python.split('.')
    python_ok = ('py3' in interpreters and abi == 'none') or ('cp312' in interpreters and abi in ('cp312', 'abi3', 'none'))
    if abi == 'abi3':
        python_ok |= any(re.fullmatch(r'cp3\d+', p) and 38 <= int(p[2:]) <= 312 for p in interpreters)
    if not python_ok:
        return False
    if platforms == 'any':
        return abi == 'none'
    for platform in platforms.split('.'):
        if platform == f'manylinux2014_{arch}':
            return True
        match = re.fullmatch(r'manylinux_2_(\d+)_' + arch, platform)
        if match and int(match[1]) <= 36:
            return True
    return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify-downloads', action='store_true')
    parser.add_argument('--check-local', action='store_true', help='offline consistency checks only; no network/download/install')
    parser.add_argument('--image-only', action='store_true', help='verify official immutable image archive while preserving already verified wheel records')
    parser.add_argument('--image-metadata-file', help='previously downloaded exact official archive; pinned SHA256 is still mandatory')
    args = parser.parse_args()
    if args.check_local:
        return check_local()
    if not args.verify_downloads:
        parser.error('network verification requires explicit --verify-downloads')
    if args.image_only:
        report_path = RUNTIME / 'linux-runtime-verification.json'
        report = json.loads(report_path.read_text())
        if any(v['status'] != 'verified' for v in report['platforms'].values()):
            raise SystemExit('Wheel verification must finish first.')
        with tempfile.TemporaryDirectory(prefix='nyamanga-official-image-') as tmp:
            previous = report['python_image']
            report['python_image'] = official_python_archive(Path(tmp), args.image_metadata_file)
            report['python_image']['registry_attempt'] = previous
        report['image_checked_at'] = datetime.now(timezone.utc).isoformat()
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
        print('Official immutable Python 3.12.0 image metadata verified; image not pulled or executed.')
        return 0
    pins = []
    for line in (RUNTIME / 'requirements-lock-macos-arm64.txt').read_text().splitlines():
        if line and not line.startswith('#'):
            pins.append(tuple(line.split()[0].split('==')))
    report = {'checked_at': datetime.now(timezone.utc).isoformat(), 'python': '3.12.0',
              'source_lock_sha256': hashlib.sha256((RUNTIME / 'requirements-lock-macos-arm64.txt').read_bytes()).hexdigest(),
              'platforms': {}, 'python_image': {}}
    with tempfile.TemporaryDirectory(prefix='nyamanga-official-runtime-') as tmp:
        temp = Path(tmp)
        metadata = {}
        for name, version in pins:
            url = f'https://pypi.org/pypi/{name}/{version}/json'
            try:
                data = json.loads(fetch(url, temp / 'metadata.json'))
                if data['info']['version'] != version:
                    raise RuntimeError('release_version_mismatch')
                metadata[name] = (url, data)
            except (RuntimeError, ValueError, KeyError) as error:
                metadata[name] = (url, str(error))
        for platform, arch in [('amd64', 'x86_64'), ('arm64', 'aarch64')]:
            found, blocked = [], []
            for name, version in pins:
                url, data = metadata[name]
                if isinstance(data, str):
                    blocked.append({'package': f'{name}=={version}', 'source': url, 'reason': data})
                    continue
                choices = sorted((f for f in data['urls'] if not f.get('yanked') and wheel_supported(f['filename'], arch)), key=lambda f: f['filename'])
                if not choices:
                    blocked.append({'package': f'{name}=={version}', 'source': url, 'reason': 'no_compatible_binary_wheel'})
                    continue
                selected = choices[0]
                try:
                    raw = fetch(selected['url'], temp / 'wheel')
                    digest = hashlib.sha256(raw).hexdigest()
                    if digest != selected['digests']['sha256'] or len(raw) != selected['size']:
                        raise RuntimeError('wheel_digest_or_size_mismatch')
                    found.append({'package': f'{name}=={version}', 'filename': selected['filename'],
                                  'sha256': digest, 'bytes': len(raw), 'source': url, 'url': selected['url'],
                                  'native_dependencies': wheel_native_dependencies(raw)})
                except RuntimeError as error:
                    blocked.append({'package': f'{name}=={version}', 'source': url, 'reason': str(error)})
            report['platforms'][platform] = {'status': 'blocked' if blocked else 'verified', 'wheels': found, 'blockers': blocked}
            target = RUNTIME / f'requirements-lock-linux-{platform}.txt'
            if not blocked:
                target.write_text('# CPython 3.12.0 / Linux glibc 2.36 (bookworm); official downloaded wheel SHA256.\n' + ''.join(f"{f['package']} --hash=sha256:{f['sha256']}\n" for f in found))
            elif target.exists():
                raise RuntimeError('refuse_to_leave_preexisting_unverified_linux_lock')
        try:
            token_data = json.loads(fetch('https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/python:pull', temp / 'token'))
            raw = fetch('https://registry-1.docker.io/v2/library/python/manifests/3.12.0-slim-bookworm', temp / 'image',
                        ('Authorization: Bearer ' + token_data['token'], 'Accept: application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json'))
            index = json.loads(raw)
            platforms = {m['platform'].get('architecture') for m in index.get('manifests', []) if m['platform'].get('os') == 'linux'}
            if not {'amd64', 'arm64'} <= platforms:
                raise RuntimeError('python_image_missing_target_architecture')
            report['python_image'] = {'status': 'verified', 'image': 'python:3.12.0-slim-bookworm@sha256:' + hashlib.sha256(raw).hexdigest(),
                                      'source': 'https://registry-1.docker.io/v2/library/python/manifests/3.12.0-slim-bookworm', 'platforms': sorted(platforms)}
        except (RuntimeError, ValueError, KeyError) as error:
            try:
                report['python_image'] = official_python_archive(temp)
                report['python_image']['registry_attempt'] = {'status': 'blocked', 'reason': str(error)}
            except RuntimeError as archive_error:
                report['python_image'] = {'status': 'blocked', 'reason': str(archive_error), 'registry_reason': str(error),
                                          'meaning': 'Official metadata could not be verified; no image digest is asserted.'}
    (RUNTIME / 'linux-runtime-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'platforms': {k: {'status': v['status'], 'wheels': len(v['wheels']), 'blockers': len(v['blockers'])} for k, v in report['platforms'].items()}, 'python_image': report['python_image']['status']}))
    return 1 if any(v['status'] != 'verified' for v in report['platforms'].values()) or report['python_image']['status'] != 'verified' else 0


if __name__ == '__main__':
    raise SystemExit(main())
