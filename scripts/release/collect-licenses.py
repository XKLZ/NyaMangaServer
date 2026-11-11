#!/usr/bin/env python3
"""Collect installed Go/npm license texts for distribution; never installs packages."""
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'third_party'


def main():
    OUT.mkdir(exist_ok=True)
    records = []
    def add(ecosystem, name, version, directory, declared=None):
        texts = []
        for path in sorted(directory.iterdir()):
            if path.is_file() and re.match(r'^(licen[cs]e|copying|notice|copyright)(?:$|[.\-_])', path.name, re.I):
                texts.append(path)
        if not texts and name == '@ant-design/icons-svg' and version == '4.6.0':
            texts = [OUT / 'upstream/ant-design-icons-svg.LICENSE']
        if not texts and name == 'is-mobile' and version == '5.0.0':
            license_text = (directory / 'README.md').read_text().split('## License\n', 1)[1].strip() + '\n'
            path = OUT / 'upstream/is-mobile.LICENSE'
            path.write_text(license_text)
            texts = [path]
        if not texts:
            raise ValueError(f'license text missing for {ecosystem}:{name}@{version}')
        slug = re.sub(r'[^A-Za-z0-9._-]', '_', name) + '-' + version
        destination = OUT / 'licenses' / ecosystem / slug
        destination.mkdir(parents=True, exist_ok=True)
        copied = []
        for path in texts:
            target = destination / path.name
            target.write_bytes(path.read_bytes())
            copied.append(str(target.relative_to(OUT)))
        records.append(dict(ecosystem=ecosystem, name=name, version=version, declared_license=declared, files=copied))
    env = dict(os.environ, GOTOOLCHAIN='local', GOPATH=str(ROOT / '.runtime/cache/go'), GOCACHE=str(ROOT / '.runtime/cache/go-build'), GOPROXY='off')
    output = subprocess.check_output(['go', 'list', '-deps', '-json', './cmd/server', './cmd/instance-restore'], cwd=ROOT, env=env, text=True)
    decoder = json.JSONDecoder()
    modules = set()
    while output.strip():
        row, end = decoder.raw_decode(output.lstrip()); output=output.lstrip()[end:]
        module = row.get('Module')
        if module and not module.get('Main') and module['Path'] not in modules:
            modules.add(module['Path'])
            if 'Dir' not in module: raise ValueError('Go module not locally installed: '+module['Path'])
            add('go', module['Path'], module['Version'], Path(module['Dir']))
    lock = json.loads((ROOT/'web/package-lock.json').read_text())
    for path, row in sorted(lock['packages'].items()):
        if not path or row.get('dev'): continue
        directory = ROOT/'web'/path
        package = json.loads((directory/'package.json').read_text())
        add('npm', package['name'], package['version'], directory, package.get('license'))
    (OUT/'manifest.json').write_text(json.dumps({'format':1,'scope':'Go modules and bundled production npm packages; runtime OS and Python notices remain in the image','packages':records},ensure_ascii=False,indent=2)+'\n')
    print(f'Collected {len(records)} package notice sets')

if __name__=='__main__': main()
