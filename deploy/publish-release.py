#!/usr/bin/env python3
"""Explicit GitHub Actions publication only; never overwrite an existing release."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.error
import urllib.request


def validate_draft(release, commit, tag, directory, names):
    """Only resume our complete, unchanged private draft; never mutate assets."""
    if not release.get('draft') or not release.get('prerelease') or release.get('tag_name') != tag or release.get('target_commitish') != commit:
        raise ValueError('existing release is not this exact unpublished candidate')
    assets = release.get('assets', [])
    if len(assets) != len(names) or {v['name'] for v in assets} != names:
        raise ValueError('partial or unexpected draft assets; manual review required')
    for asset in assets:
        path = directory / asset['name']
        with path.open('rb') as stream:
            digest = 'sha256:' + hashlib.file_digest(stream, 'sha256').hexdigest()
        if asset.get('digest') != digest or asset.get('size') != path.stat().st_size or asset.get('state') != 'uploaded':
            raise ValueError('draft asset identity is unverified; never replace it')


def validate_existing_image(manifest, expected_id, has_verified_draft):
    if not has_verified_draft or manifest.get('config', {}).get('digest') != expected_id:
        raise ValueError('image tag exists and is not this verified partial publication')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--directory', type=Path, required=True)
    p.add_argument('--commit', required=True)
    args = p.parse_args()
    if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('GITHUB_REPOSITORY') != 'XKLZ/NyaMangaServer' or os.environ.get('GITHUB_REF') != 'refs/heads/main':
        raise ValueError('publication only in the explicitly dispatched main release job')
    directory = args.directory.resolve()
    hashes = {}
    for line in (directory / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split('  ', 1)
        if not re.fullmatch(r'[0-9a-f]{64}', digest) or Path(name).name != name or name in hashes:
            raise ValueError('invalid asset manifest')
        path = directory / name
        if path.is_symlink():
            raise ValueError('asset links forbidden')
        with path.open('rb') as stream:
            if hashlib.file_digest(stream, 'sha256').hexdigest() != digest:
                raise ValueError('asset digest mismatch')
        hashes[name] = digest
    receipt = json.loads((directory / 'build-identity.json').read_text())
    version = receipt['version']
    if not re.fullmatch(r'\d+\.\d+\.\d+-beta\.\d+', version) or receipt['commit'] != args.commit or not re.fullmatch(r'[0-9a-f]{40}', args.commit):
        raise ValueError('release identity mismatch')
    tag = 'v' + version
    image = 'ghcr.io/xklz/nyamangaserver:' + version
    if receipt['image'] != image or receipt['platform'] != 'linux/amd64':
        raise ValueError('unexpected image target')
    expected = {f'nyamangaserver-{version}-linux-amd64.tar.gz', f'nyamangaserver-{version}-install.tar.gz', 'build-identity.json', 'RELEASE-NOTES.md'}
    if set(hashes) != expected or {p.name for p in directory.iterdir()} != expected | {'SHA256SUMS'}:
        raise ValueError('unexpected release assets')
    token = os.environ['GH_TOKEN']
    remote = {}
    for resource in ('releases/tags/' + tag, 'git/ref/tags/' + tag):
        req = urllib.request.Request('https://api.github.com/repos/XKLZ/NyaMangaServer/' + resource, headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json'})
        try:
            with urllib.request.urlopen(req, timeout=30) as response:
                remote[resource] = json.load(response)
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise RuntimeError('cannot verify remote release absence') from None
    draft = remote.get('releases/tags/' + tag)
    if draft:
        validate_draft(draft, args.commit, tag, directory, expected | {'SHA256SUMS'})
    reference = remote.get('git/ref/tags/' + tag)
    if reference and (not draft or reference.get('object', {}).get('type') != 'commit' or reference['object']['sha'] != args.commit):
        raise ValueError('existing tag is not this verified partial publication')
    subprocess.run(['docker', 'load', '-i', str(directory / f'nyamangaserver-{version}-linux-amd64.tar.gz')], check=True)
    info = json.loads(subprocess.check_output(['docker', 'image', 'inspect', image]))[0]
    if info['Id'] != receipt['image_id'] or info['Config']['Labels'].get('org.opencontainers.image.revision') != args.commit:
        raise ValueError('loaded image differs from tested artifact')
    with tempfile.TemporaryDirectory(prefix='nyamanga-publish-') as config:
        env = dict(os.environ, DOCKER_CONFIG=config)
        subprocess.run(['docker', 'login', 'ghcr.io', '-u', os.environ['GITHUB_ACTOR'], '--password-stdin'], input=token.encode(), env=env, check=True, stdout=subprocess.DEVNULL)
        existing = subprocess.run(['docker', 'manifest', 'inspect', image], env=env, capture_output=True, text=True)
        image_exists = existing.returncode == 0
        if image_exists:
            validate_existing_image(json.loads(existing.stdout), receipt['image_id'], bool(draft))
        elif not any(marker in existing.stderr.lower() for marker in ('manifest unknown', 'no such manifest', 'name unknown')):
            raise RuntimeError('registry absence not proven; inspect authentication/network, do not retry blindly')
        # Reserve a draft first. Any partial failure leaves it visible for review.
        if not draft:
            subprocess.run(['gh', 'release', 'create', tag, '--repo', 'XKLZ/NyaMangaServer', '--target', args.commit, '--draft', '--prerelease', '--title', tag, '--notes-file', str(directory / 'RELEASE-NOTES.md'), *[str(directory / name) for name in sorted(expected | {'SHA256SUMS'})]], check=True)
        if not image_exists:
            subprocess.run(['docker', 'push', image], env=env, check=True)
    # Require public package visibility before making the Release public. The
    # first package may need its owner to change visibility; failure is deliberate.
    with tempfile.TemporaryDirectory(prefix='nyamanga-anonymous-pull-') as config:
        subprocess.run(['docker', 'pull', image], env=dict(os.environ, DOCKER_CONFIG=config), check=True)
    subprocess.run(['gh', 'release', 'edit', tag, '--repo', 'XKLZ/NyaMangaServer', '--draft=false'], check=True)
    print('Published tested beta; verify anonymous Release downloads and SHA256 separately')


if __name__ == '__main__':
    main()
