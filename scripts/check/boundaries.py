"""Check actual Go imports and production service boundaries without running services."""
import json
import pathlib
import subprocess
import argparse
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source-only', action='store_true', help='allow the missing generated frontend embed, but no other package errors')
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[2]
output = subprocess.check_output(['go', 'list', *(['-e'] if args.source_only else []), '-json', './cmd/...', './internal/...'], cwd=root, text=True)
decoder = json.JSONDecoder()
while output.strip():
    package, offset = decoder.raw_decode(output.lstrip())
    output = output.lstrip()[offset:]
    errors = ([package['Error']] if package.get('Error') else []) + package.get('DepsErrors', [])
    for error in errors:
        stack = error.get('ImportStack', [])
        asset_package = package.get('Module', {}).get('Path', '') + '/internal/webassets'
        if args.source_only and stack and stack[-1] == asset_package and error['Err'] == 'pattern all:dist: no matching files found':
            continue
        raise SystemExit(error['Err'])
    name = package['ImportPath'].split('/internal/')[-1]
    imports = package.get('Imports', [])
    forbidden = ['internal/testfixture', '/experiments/']
    if name in ('httpapi', 'comic', 'library', 'auth', 'model', 'metadata', 'tagcatalog', 'tagimages', 'provider', 'organization', 'search', 'userstate', 'maintenance', 'recommendation', 'recommendationpilot', 'importflow', 'enhancements', 'galleryhighlight'):
        forbidden += ['internal/store', 'database/sql', 'modernc.org/sqlite']
    if name == 'model':
        forbidden += ['net/http', 'internal/httpapi']
    if package['ImportPath'].endswith('/cmd/fixtures'):
        forbidden = []
    for dependency in imports:
        if any(x in dependency for x in forbidden):
            raise SystemExit(f"{package['ImportPath']} imports forbidden {dependency}")
print('Go dependency boundaries: PASS')
