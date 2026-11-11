#!/usr/bin/env python3
"""Export a previously tested image and allowlisted installation files; no publish."""
import argparse
import gzip
import hashlib
import json
import io
from pathlib import Path
import re
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]


def installation_bundle(directory, version):
    allowed = ['deploy/configure.py', 'deploy/compose.release.yaml', 'deploy/compose.library.yaml', 'deploy/config.example.json', 'deploy/README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md']
    allowed += [str(path.relative_to(ROOT)) for path in sorted((ROOT / 'third_party').rglob('*')) if path.is_file()]
    with tarfile.open(directory / f'nyamangaserver-{version}-install.tar.gz', 'w:gz') as bundle:
        for name in allowed:
            # Standalone install kits have no source tree. Link supplemental
            # documentation to the exact release tag rather than broken paths.
            if name in ('deploy/README.md', 'THIRD_PARTY_NOTICES.md'):
                def link(match):
                    target = match[2]
                    if '://' in target or target.startswith('#'):
                        return match[0]
                    path, *fragment = target.split('#', 1)
                    relative = (ROOT / name).parent.joinpath(path).resolve().relative_to(ROOT)
                    suffix = '#' + fragment[0] if fragment else ''
                    return f'[{match[1]}](https://github.com/XKLZ/NyaMangaServer/blob/v{version}/{relative}{suffix})'
                body = re.sub(r'\[([^\]\n]*)\]\(([^)\n]+)\)', link, (ROOT / name).read_text()).encode()
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = len(body), 0o644
                bundle.addfile(entry, io.BytesIO(body))
            else:
                def public_metadata(entry):
                    entry.uid = entry.gid = entry.mtime = 0
                    entry.uname = entry.gname = ''
                    entry.mode = 0o644
                    entry.pax_headers = {}
                    return entry
                bundle.add(ROOT / name, arcname=name, recursive=False, filter=public_metadata)


def write_checksums(directory):
    with (directory / 'SHA256SUMS').open('x') as sums:
        for path in sorted(directory.iterdir()):
            if path.name == 'SHA256SUMS':
                continue
            if path.stat().st_size >= 2 * 1024**3:
                raise ValueError('asset exceeds single GitHub Release file limit')
            with path.open('rb') as stream:
                digest = hashlib.file_digest(stream, 'sha256').hexdigest()
            sums.write(f'{digest}  {path.name}\n')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--directory', type=Path, required=True)
    p.add_argument('--install-only', action='store_true', help='local installation preview; no image or Docker access')
    args = p.parse_args()
    version = subprocess.check_output(['python3', str(ROOT / 'scripts/check/release-version.py')], text=True).strip()
    commit = subprocess.check_output(['python3', str(ROOT / 'scripts/check/build-identity.py')], text=True).strip()
    if len(commit) != 40:
        raise ValueError('clean committed release required')
    if args.install_only:
        directory = args.directory.resolve()
        directory.mkdir(parents=True, exist_ok=False)
        installation_bundle(directory, version)
        (directory / 'preview-identity.json').write_text(json.dumps({'version': version, 'commit': commit, 'scope': 'installation preview only; no image'}, indent=2) + '\n')
        write_checksums(directory)
        print('Installation preview created; no image built or published')
        return
    tag = 'ghcr.io/xklz/nyamangaserver:' + version
    info = json.loads(subprocess.check_output(['docker', 'image', 'inspect', tag]))[0]
    if info['Architecture'] != 'amd64' or info['Config']['User'] != '65532:65532' or info['Config']['Labels'].get('org.opencontainers.image.revision') != commit or info['Config']['Labels'].get('org.opencontainers.image.version') != version:
        raise ValueError('image identity does not match release source')
    directory = args.directory.resolve()
    directory.mkdir(parents=True, exist_ok=False)
    archive = directory / f'nyamangaserver-{version}-linux-amd64.tar.gz'
    with archive.open('xb') as raw:
        with gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as compressed:
            process = subprocess.Popen(['docker', 'save', tag], stdout=subprocess.PIPE)
            try:
                shutil.copyfileobj(process.stdout, compressed)
            finally:
                process.stdout.close()
            if process.wait() != 0:
                raise RuntimeError('docker save failed; incomplete artifacts must not be published')
    installation_bundle(directory, version)
    receipt = {'format': 1, 'version': version, 'commit': commit, 'image': tag, 'image_id': info['Id'], 'platform': 'linux/amd64'}
    (directory / 'build-identity.json').write_text(json.dumps(receipt, indent=2) + '\n')
    shutil.copyfile(ROOT / f'docs/releases/v{version}.md', directory / 'RELEASE-NOTES.md')
    write_checksums(directory)
    print('Exported tested image and install bundle; nothing uploaded')


if __name__ == '__main__':
    main()
