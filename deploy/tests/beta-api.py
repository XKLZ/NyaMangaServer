#!/usr/bin/env python3
"""Small production-image acceptance; explicit fresh anonymous instance only.

Never initializes a nonempty instance. Credentials and exact object receipts stay
in a private directory; stdout contains only check names and counts.
"""
import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request


class Client:
    def __init__(self, origin):
        self.origin = origin.rstrip('/')
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(self.jar))
        self.csrf = ''

    def call(self, method, path, body=None, expected=200, headers=None, raw=False, retry_rate_limit=True):
        hdr = {'Origin': self.origin, 'X-CSRF-Token': self.csrf}
        data = body
        if body is not None and not isinstance(body, bytes):
            data = json.dumps(body).encode()
            hdr['Content-Type'] = 'application/json'
        hdr.update(headers or {})
        request = urllib.request.Request(self.origin + '/api/v1' + path, data, hdr, method=method)
        try:
            response = self.opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            payload = response.read()
            if response.status != expected:
                code = ''
                try:
                    code = json.loads(payload).get('error', {}).get('code', '')
                except (ValueError, AttributeError):
                    pass
                if response.status == 429 and code == 'rate_limited' and retry_rate_limit:
                    delay = int(response.headers.get('Retry-After', '60'))
                    assert 1 <= delay <= 60
                    time.sleep(delay)
                    return self.call(method, path, body, expected, headers, raw, False)
                raise AssertionError(f'{method} {path}: status={response.status} expected={expected} code={code}')
            if raw:
                return payload, dict(response.headers)
            value = json.loads(payload) if payload else None
            if isinstance(value, dict) and 'csrf_token' in value:
                self.csrf = value['csrf_token']
            return value

    def job(self, identity, accepted=('completed',), timeout=120):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            result = self.call('GET', '/jobs/' + identity)
            if result['state'] in ('completed', 'failed', 'cancelled', 'paused'):
                assert result['state'] in accepted, ('job terminal', result['state'], result.get('error_code'))
                return result
            time.sleep(.3)
        raise AssertionError('anonymous job timeout')

    def scan(self, identity):
        result = self.job(identity, accepted=('completed', 'failed'))
        items = self.call('GET', '/jobs/' + identity + '/items?page_size=100')['items']
        assert len(items) == 7
        bad = [i for i in items if i['relative_path'] == 'Broken.cbz']
        assert len(bad) == 1 and bad[0]['error_code'] == 'invalid_archive'
        normal = [i for i in items if i['relative_path'] != 'Broken.cbz']
        assert len(normal) == 6
        assert all(not i['error_code'] and not (i.get('scan_result') or {}).get('issues') for i in normal)
        return result


def private_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def initialize(args):
    root = args.evidence.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    api = Client(args.url)
    assert api.call('GET', '/auth/status')['initialized'] is False
    api.call('GET', '/diagnostics', expected=401)
    credentials = json.loads(args.credentials_input.read_text()) if args.credentials_input else {'username': 'admin', 'password': secrets.token_urlsafe(28)}
    assert credentials['username'] == 'admin' and len(credentials['password']) >= 12
    private_json(root / 'credentials.json', credentials)
    api.call('POST', '/auth/setup', {'setup_token': args.token_file.read_text().strip(), 'password': credentials['password']})
    api.call('GET', '/auth/session')
    assert next(iter(api.jar)).has_nonstandard_attr('HttpOnly')
    diagnostics = api.call('GET', '/diagnostics')
    assert diagnostics['schema_version'] == 27
    features = api.call('GET', '/settings/enhancements')
    assert not features['features']['work_recommendations']['enabled']
    assert not features['features']['series_recommendations']['enabled']
    private_json(root / 'initial-capabilities.json', features)
    lib = api.call('POST', '/libraries', {'root_key': 'anonymous', 'display_name': 'Beta anonymous'}, 201)
    scan = api.call('POST', f'/libraries/{lib["id"]}/scans', {}, 202)
    scan_result = api.scan(scan['job_id'])
    private_json(root / 'scan-result.json', scan_result)
    books = api.call('GET', '/books?state=available&library_id=' + lib['id'])['items']
    assert len(books) == 6, ('available books', len(books))
    print('PASS fresh setup, schema, six anonymous archives and bad archive isolation', flush=True)
    before = {b['id']: api.call('GET', '/books/' + b['id']) for b in books}
    first = before[books[0]['id']]
    api.call('PATCH', '/books/' + first['id'] + '/overrides', {
        'expected_revision': first['revision'], 'fields': {
            'title': {'op': 'set', 'value': 'Beta edition'},
            'title_original': {'op': 'set', 'value': '匿名テスト作品'},
            'tags': {'op': 'set', 'value': [{'namespace': 'parody', 'name': 'beta original'}, {'namespace': 'character', 'name': 'beta character'}, {'namespace': 'artist', 'name': 'beta author'}]},
        }})
    api.call('PATCH', '/books/' + first['id'] + '/user-state', {'expected_revision': 0, 'favorite': True})
    reading = api.call('GET', '/editions/' + first['id'] + '/reading-state')
    private_json(root / 'reading-before.json', reading)
    context = api.call('GET', '/work-organization/context?edition_ids=' + ','.join(b['id'] for b in books[:2]))
    target, source = [next(e['work_id'] for e in context['editions'] if e['id'] == b['id']) for b in books[:2]]
    preview = api.call('POST', '/work-organization/preview', {'action': 'work_merge', 'id': target, 'work_ids': [source], 'expected_revisions': context['expected_revisions']})
    api.call('POST', '/works/' + target + '/merge', preview['command'])
    series = api.call('POST', '/series', {'library_id': lib['id'], 'name': 'Beta series', 'work_ids': [], 'expected_revisions': {}})
    context = api.call('GET', f'/work-organization/context?work_ids={target}&series_ids={series["id"]}')
    api.call('PUT', '/series/' + series['id'] + '/members', {'work_ids': [target], 'expected_revisions': context['expected_revisions']})
    scan2 = api.call('POST', f'/libraries/{lib["id"]}/scans', {}, 202)
    api.scan(scan2['job_id'])
    after = api.call('GET', '/books/' + first['id'])
    assert after['title_original'] == '匿名テスト作品' and after['user_state']['favorite']
    assert after['content_revision'] == first['content_revision']
    members = api.call('GET', '/series/' + series['id'] + '/members')
    assert [m['id'] for m in members['items']] == [target]
    private_json(root / 'objects.json', {'library': lib, 'edition_ids': [b['id'] for b in books], 'work_id': target, 'series_id': series['id'], 'before': before, 'after': after})
    print('PASS metadata override, personal favorite, Work/Series and no-change rescan preservation', flush=True)
    further_checks(api, args, root, lib, first, credentials)


def further_checks(api, args, root, lib, first, credentials):
    edition_path = '/editions/' + first['id']
    if not (root / 'tag-result.json').exists():
        reading = api.call('GET', edition_path + '/reading-state')
        write = {'expected_revision': reading['revision'], 'content_revision': reading['current_content_revision'],
                 'parsing_revision': reading['current_parsing_revision'], 'idempotency_key': 'beta-reading-complete', 'action': 'complete'}
        completed = api.call('PUT', edition_path + '/reading-state', write)
        assert completed['completed'] and completed['read_count'] == 0
        assert api.call('PUT', edition_path + '/reading-state', write) == completed
        tags = api.call('GET', '/tag-catalog?' + urllib.parse.urlencode({'library_id': lib['id'], 'category': 'parody'}))
        tag = next(t for t in tags['items'] if t['name'] == 'beta original')
        translated = api.call('PUT', '/tags/' + tag['id'] + '/translations/zh-CN', {'expected_revision': tag['translation_revision'], 'text': '匿名原作'})
        followed = api.call('PUT', '/tags/' + tag['id'] + '/follow', {'expected_revision': tag['follow_revision']})
        assert followed['followed']
        images = api.call('GET', '/tag-catalog/' + tag['id'] + '/images')
        image = api.call('PUT', '/tag-catalog/' + tag['id'] + '/images/foreground', args.image.read_bytes(), headers={'Content-Type': 'image/png', 'X-Expected-Revision': str(images['foreground']['revision'])})
        private_json(root / 'tag-result.json', {'tag': tag, 'translation': translated, 'image': image})
    reading = api.call('GET', edition_path + '/reading-state')
    assert reading['completed'] and reading['read_count'] == 0
    saved_tag = json.loads((root / 'tag-result.json').read_text())
    current_tag = api.call('GET', '/tag-catalog/' + saved_tag['tag']['id'] + '?library_id=' + lib['id'])
    assert current_tag['followed'] and current_tag['translation'] == '匿名原作'
    current_images = api.call('GET', '/tag-catalog/' + saved_tag['tag']['id'] + '/images')
    assert current_images['foreground']['asset_id']
    cover, _ = api.call('GET', '/books/' + first['id'] + '/cover?content_revision=' + str(first['content_revision']), raw=True)
    assert cover
    private_json(root / 'cover.json', {'edition_id': first['id'], 'sha256': hashlib.sha256(cover).hexdigest()})
    print('PASS tag translation, image, follow and idempotent personal reading', flush=True)
    progress_path = root / 'account-progress.json'
    progress = json.loads(progress_path.read_text()) if progress_path.exists() else {}
    accounts = progress.get('completed', [])
    for username, grants in [('beta_viewer', {lib['id']: 'viewer'}), ('beta_editor', {lib['id']: 'editor'}), ('beta_denied', {})]:
        if any(saved['username'] == username for saved in accounts):
            continue
        if progress.get('username') == username:
            assert progress['phase'] in ('created', 'password_changed'), 'inspect uncertain account creation'
            initial, password = progress['initial_password'], progress['password']
            account = next(v for v in api.call('GET', '/accounts')['items'] if v['id'] == progress['id'])
        else:
            initial, password = secrets.token_urlsafe(25), secrets.token_urlsafe(25)
            progress = {'completed': accounts, 'username': username, 'initial_password': initial, 'password': password, 'phase': 'creating'}
            private_json(progress_path, progress)
            account = api.call('POST', '/accounts', {'username': username, 'password': initial}, 201)
            progress.update(id=account['id'], phase='created')
            private_json(progress_path, progress)
            api.call('PUT', '/accounts/' + account['id'], {'expected_revision': account['revision'], 'role': 'user', 'enabled': True, 'library_grants': grants}, 204)
        if progress['phase'] == 'created':
            user = Client(args.url)
            user.call('POST', '/auth/login', {'username': username, 'password': initial})
            user.call('PUT', '/auth/password', {'current_password': initial, 'new_password': password})
            progress['phase'] = 'password_changed'
            private_json(progress_path, progress)
        user = Client(args.url)
        user.call('POST', '/auth/login', {'username': username, 'password': password})
        user.call('GET', '/settings/enhancements', expected=403)
        if grants:
            user.call('GET', '/books/' + first['id'])
            user.call('GET', '/books/' + first['id'] + '/cover?content_revision=' + str(first['content_revision']), raw=True)
            assert user.call('GET', edition_path + '/reading-state')['completed'] is False
            assert user.call('GET', '/books?library_id=' + lib['id'])['total'] >= 6
            if username == 'beta_viewer':
                user.call('POST', '/libraries/' + lib['id'] + '/scans', {}, 403)
            else:
                job = user.call('POST', '/libraries/' + lib['id'] + '/scans', {}, 202)
                user.scan(job['job_id'])
        else:
            assert user.call('GET', '/libraries')['items'] == []
            user.call('GET', '/books/' + first['id'], expected=404)
            user.call('GET', '/books/' + first['id'] + '/cover?content_revision=' + str(first['content_revision']), expected=404)
        accounts.append({'username': username, 'password': password, 'id': account['id']})
        progress['completed'] = accounts
        private_json(progress_path, progress)
    private_json(root / 'test-accounts.json', accounts)
    (root / 'account-progress.json').unlink()
    print('PASS viewer/editor/no-grant boundaries, image access and personal-state isolation', flush=True)
    check = api.call('POST', '/maintenance/health-checks', {'request_key': 'beta-health-quick', 'mode': 'quick'}, 202)
    api.job(check['job_id'])
    private_json(root / 'health.json', api.call('GET', '/maintenance/health-checks/' + check['id']))
    plan = api.call('POST', '/maintenance/plans', {'operation': 'cleanup', 'expected_revisions': {}, 'options': {'categories': ['covers']}})
    cleanup = api.call('POST', '/maintenance/plans/' + plan['id'] + '/execute', {}, 202)
    api.job(cleanup['job_id'])
    private_json(root / 'cleanup.json', api.call('GET', '/maintenance/plans/' + plan['id']))
    api.call('GET', '/books/' + first['id'])
    print('PASS anonymous quick health and selected cover-cache cleanup', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--resume', action='store_true', help='continue only after the persisted organization checkpoint')
    parser.add_argument('--url', required=True)
    parser.add_argument('--token-file', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--image', type=Path, required=True, help='synthetic image generated by prepare-beta.py')
    parser.add_argument('--credentials-input', type=Path, help='optional private anonymous test credentials, never printed')
    args = parser.parse_args()
    origin = urllib.parse.urlsplit(args.url)
    if origin.scheme not in ('http', 'https') or origin.username or origin.password or origin.path not in ('', '/'):
        parser.error('explicit clean origin required')
    os.umask(0o077)
    if args.resume:
        saved = json.loads((args.evidence / 'objects.json').read_text())
        credentials = json.loads((args.evidence / 'credentials.json').read_text())
        api = Client(args.url)
        api.call('POST', '/auth/login', credentials)
        further_checks(api, args, args.evidence, saved['library'], saved['before'][saved['edition_ids'][0]], credentials)
    else:
        initialize(args)


if __name__ == '__main__':
    main()
