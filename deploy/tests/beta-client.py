#!/usr/bin/env python3
"""Exercise native client contracts on an explicitly prepared anonymous release instance."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import time
import urllib.request

spec = importlib.util.spec_from_file_location('beta_api', Path(__file__).with_name('beta-api.py'))
api_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api_module)


class Device(api_module.Client):
    def __init__(self, origin):
        super().__init__(origin)
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.token = ''

    def call(self, method, path, body=None, expected=200, headers=None, raw=False, retry_rate_limit=True):
        headers = dict(headers or {})
        if self.token:
            headers['Authorization'] = 'Bearer ' + self.token
        return super().call(method, path, body, expected, headers, raw, retry_rate_limit)

    def login(self, credentials):
        result = self.call('POST', '/auth/device-sessions', credentials | {
            'device_name': 'Anonymous release device', 'client_instance_id': 'release-fixture'}, 201)
        self.token = result['token']
        return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--url', required=True)
    p.add_argument('--evidence', required=True, type=Path)
    p.add_argument('--restored', action='store_true')
    args = p.parse_args()
    root = args.evidence
    objects = json.loads((root / 'objects.json').read_text())
    account = next(a for a in json.loads((root / 'test-accounts.json').read_text()) if a['username'] == 'beta_viewer')
    api = Device(args.url)
    receipt_path = root / 'client-protocol.json'
    if args.restored:
        old = json.loads(receipt_path.read_text())
        api.token = old['token']
        api.call('GET', '/client/capabilities', expected=401)
    login = api.login({key: account[key] for key in ('username', 'password')})
    caps = api.call('GET', '/client/capabilities')
    for feature in ('downloads', 'offline_manifests', 'offline_sync'):
        assert caps['features'][feature]['supported']
    if args.restored:
        assert caps['instance']['instance_id'] == old['queue']['instance_id']
        assert caps['instance']['cache_epoch'] != old['queue']['cache_epoch']
        api.call('POST', '/client/offline/operation-status', old['queue'] | {'operation_ids': [old['operation_id']]}, 409)
        state = api.call('GET', '/editions/' + old['edition_id'] + '/reading-state')
        assert state['read_count'] == old['reading']['read_count'] and state['completed'] == old['reading']['completed']
        print('PASS restored device invalidation, cache generation and offline reading preservation', flush=True)
        api.call('POST', '/auth/logout', {}, 204)
        return
    api.call('GET', '/settings/enhancements', expected=403)
    library = objects['library']['id']
    hits = api.call('POST', '/client/works/search', {'library_id': library, 'page_size': 24})
    hit = next(x for x in hits['items'] if x['work_id'] == objects['work_id'])
    context = api.call('GET', '/client/works/' + hit['work_id'] + '/reading-context?edition_id=' + hit['entry_edition_id'])
    edition = context['edition']['id']
    versions = {key: context['edition'][key] for key in ('content_revision', 'parsing_revision')}
    manifest = api.call('POST', '/client/editions/' + edition + '/offline-manifests', versions)
    pages = api.call('GET', '/client/offline-manifests/' + manifest['id'] + '/pages?page_size=100')
    assert len(pages['items']) == pages['total'] > 0
    for page in (pages['items'][0], pages['items'][-1]):
        path = page['image_url'].removeprefix('/api/v1')
        data, headers = api.call('GET', path, headers={'X-Nya-Image-Purpose': 'prefetch'}, raw=True)
        headers = {k.lower(): v for k, v in headers.items()}
        assert headers['etag'] == '"' + hashlib.sha256(data).hexdigest() + '"'
        body, _ = api.call('GET', path, expected=304, headers={'X-Nya-Image-Purpose': 'prefetch', 'If-None-Match': headers['etag']}, raw=True)
        assert not body
    assert api.call('GET', '/editions/' + edition + '/reading-state')['read_count'] == context['reading']['read_count']
    prepared = api.call('POST', '/client/editions/' + edition + '/downloads', {
        'content_revision': versions['content_revision'], 'idempotency_key': 'release-original-download'}, 202)
    end = time.monotonic() + 60
    while prepared['state'] == 'preparing' and time.monotonic() < end:
        time.sleep(.1)
        prepared = api.call('GET', '/client/downloads/' + prepared['id'])
    assert prepared['state'] == 'ready'
    download_path = '/client/downloads/' + prepared['id'] + '/content'
    data, _ = api.call('GET', download_path, raw=True)
    assert len(data) == prepared['length'] and hashlib.sha256(data).hexdigest() == prepared['sha256']
    offset = len(data) // 2
    resumed, _ = api.call('GET', download_path, expected=206, headers={
        'Range': f'bytes={offset}-', 'If-Range': '"' + prepared['sha256'] + '"'}, raw=True)
    assert resumed == data[offset:]
    queue = caps['instance'] | {'account_id': login['actor']['id'], 'client_instance_id': 'release-fixture'}
    current = api.call('GET', '/editions/' + edition + '/reading-state')
    operations = [
        {'operation_id': 'release-offline-restart', 'local_sequence': 1, 'object_id': edition, 'type': 'restart',
         'expected_revision': current['revision'], 'offline_session_id': 'release-offline-session', 'body': versions},
        {'operation_id': 'release-offline-complete', 'local_sequence': 2, 'object_id': edition, 'type': 'complete',
         'previous_operation_id': 'release-offline-restart', 'offline_session_id': 'release-offline-session',
         'body': versions | {'page_index': pages['total'] - 1}}]
    request = queue | {'operations': operations}
    results = api.call('POST', '/client/offline/operations', request)['items']
    assert all(x['status'] == 'applied' for x in results)
    assert api.call('POST', '/client/offline/operations', request)['items'] == results
    reading = api.call('GET', '/editions/' + edition + '/reading-state')
    assert reading['completed'] and reading['read_count'] == current['read_count'] + 1
    api_module.private_json(receipt_path, {'token': api.token, 'queue': queue, 'edition_id': edition,
                                          'operation_id': operations[-1]['operation_id'], 'reading': reading})
    denied = next(a for a in json.loads((root / 'test-accounts.json').read_text()) if a['username'] == 'beta_denied')
    other = Device(args.url)
    other.login({key: denied[key] for key in ('username', 'password')})
    other.call('GET', '/client/downloads/' + prepared['id'], expected=404)
    other.call('GET', '/client/offline-manifests/' + manifest['id'], expected=404)
    other.call('POST', '/auth/logout', {}, 204)
    print('PASS device scope, manifest images and 304, original Range/SHA256, offline replay and cross-account rejection', flush=True)


if __name__ == '__main__':
    main()
