"""Anonymous M1 container acceptance. Run on the Docker host, never with user data."""
import argparse
import http.cookiejar
import json
import os
import pathlib
import secrets
import subprocess
import urllib.error
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--test-root', required=True)
parser.add_argument('--compose', required=True)
parser.add_argument('--token-file', required=True)
parser.add_argument('--url', default='http://localhost:9400')
args = parser.parse_args()
test_root = pathlib.Path(args.test_root).resolve()
if test_root in (pathlib.Path('/'), pathlib.Path.home()):
    raise SystemExit('explicit isolated test root required')
for filename in [args.compose, args.token_file]:
    if not pathlib.Path(filename).resolve().is_relative_to(test_root):
        raise SystemExit('acceptance materials must be within test-root')
compose = ['docker', 'compose', '-f', str(pathlib.Path(args.compose).resolve())]
origin = args.url.rstrip('/')
password = secrets.token_urlsafe(24)
next_password = secrets.token_urlsafe(24)
jar = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar))
csrf = ''

def call(method, path, body=None, expected=200):
    global csrf
    headers = {'Origin': origin}
    if csrf:
        headers['X-CSRF-Token'] = csrf
    data = None
    if body is not None:
        headers['Content-Type'] = 'application/json'
        data = json.dumps(body).encode()
    request = urllib.request.Request(origin + '/api/v1' + path, data, headers, method=method)
    try:
        response = client.open(request, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.status == expected, (method, path, response.status, expected)
        raw = response.read()
        result = json.loads(raw) if raw else None
        if isinstance(result, dict) and 'csrf_token' in result:
            csrf = result['csrf_token']
        if expected >= 400:
            assert result['error']['request_id'] == response.headers['X-Request-ID']
        return result

assert call('GET', '/auth/status') == {'initialized': False}, 'requires a fresh project data volume'
call('GET', '/diagnostics', expected=401)
call('POST', '/auth/setup', {'setup_token': pathlib.Path(args.token_file).read_text().strip(), 'password': password})
assert len(list(jar)) == 1
cookie = next(iter(jar))
assert cookie.has_nonstandard_attr('HttpOnly') and cookie.get_nonstandard_attr('SameSite') == 'Lax'
call('GET', '/auth/session')
assert call('GET', '/library-roots') == {'items': []}
assert call('GET', '/diagnostics')['schema_version'] >= 1
call('GET', '/unimplemented-auth-probe', expected=404)
with client.open(origin + '/settings/security', timeout=10) as page:
    assert page.headers['Content-Type'].startswith('text/html')
    assert b'NyaManga' in page.read()
print('fresh-volume setup / protected API / SPA refresh: PASS')
subprocess.run(compose + ['restart'], check=True)
subprocess.run(compose + ['up', '-d', '--no-build', '--wait'], check=True)
call('GET', '/auth/session')
assert call('GET', '/auth/status') == {'initialized': True}
print('restart preserves account and session: PASS')
call('PUT', '/auth/password', {'current_password': password, 'new_password': next_password})
jar.set_cookie(cookie)  # Replay the revoked cookie, without printing it.
call('GET', '/auth/session', expected=401)
jar.clear()
call('POST', '/auth/login', {'password': password}, expected=401)
call('POST', '/auth/login', {'password': next_password})
call('POST', '/auth/logout', expected=204)
call('GET', '/auth/session', expected=401)
print('password change / revoked session / old credential / login / logout: PASS')
container = subprocess.check_output(compose + ['ps', '-q', 'server'], text=True).strip()
info = json.loads(subprocess.check_output(['docker', 'inspect', container]))[0]
assert info['Config']['User'] == '65532:65532'
assert info['State']['Health']['Status'] == 'healthy'
volume = next(v['Source'] for v in info['Mounts'] if v['Destination'] == '/data')
assert os.stat(volume).st_mode & 0o777 == 0o700
assert os.stat(pathlib.Path(volume) / 'app.sqlite').st_uid == 65532
assert os.stat(pathlib.Path(volume) / 'app.sqlite').st_mode & 0o777 == 0o600
print('non-root database ownership / private permissions / readiness: PASS')
subprocess.run(compose + ['stop', '-t', '12'], check=True)
stopped = json.loads(subprocess.check_output(['docker', 'inspect', container]))[0]
assert stopped['State']['ExitCode'] == 0 and not stopped['State']['OOMKilled']
logs = subprocess.check_output(['docker', 'logs', container], text=True, stderr=subprocess.STDOUT)
assert 'server stopped' in logs
for secret in [password, next_password, cookie.value, pathlib.Path(args.token_file).read_text().strip()]:
    assert secret not in logs, 'sensitive value appeared in logs'
print('SIGTERM graceful exit 0 / redacted logs: PASS')
subprocess.run(compose + ['down'], check=True)  # Keep only the project data volume and image.
print('project temporary container/network removed; data volume retained: PASS')
