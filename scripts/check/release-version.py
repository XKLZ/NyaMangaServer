#!/usr/bin/env python3
"""Check release metadata against the shared server/restore version; no writes."""
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]


def check(root=ROOT):
    version = (root / 'internal/buildinfo/VERSION').read_text().strip()
    if not re.fullmatch(r'\d+\.\d+\.\d+(?:-(?:beta|rc)\.\d+)?', version):
        raise ValueError('invalid release version')
    package = json.loads((root / 'web/package.json').read_text())
    lock = json.loads((root / 'web/package-lock.json').read_text())
    api = json.loads((root / 'api/openapi.yaml').read_text())
    if any(value != version for value in (package['version'], lock['version'], lock['packages']['']['version'], api['info']['version'])):
        raise ValueError('server, web, lock and API versions must agree')
    for name in ('server', 'instance-restore'):
        if 'var version = buildinfo.Version' not in (root / f'cmd/{name}/main.go').read_text():
            raise ValueError('paired programs must use buildinfo.Version')
    if f'ghcr.io/xklz/nyamangaserver:{version}\n' not in (root / 'deploy/compose.release.yaml').read_text():
        raise ValueError('installation image version differs from application')
    return version


if __name__ == '__main__':
    print(check())
