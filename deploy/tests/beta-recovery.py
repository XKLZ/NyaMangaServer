#!/usr/bin/env python3
"""Authenticated anonymous backup/restore phases against the production image.

Process restart and the isolated Compose target are controlled separately. No
Docker or live database writes are performed by this HTTP client.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import time

spec = importlib.util.spec_from_file_location('beta_api', Path(__file__).with_name('beta-api.py'))
api_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api_module)
Client, private_json = api_module.Client, api_module.private_json


def login(args, credentials):
    api = Client(args.url)
    api.call('POST', '/auth/login', credentials)
    return api


def wait(api, kind, identity, state):
    for _ in range(600):
        result = api.call('GET', '/maintenance/' + kind + '/' + identity)
        operation = result.get('operation', result)
        if operation['state'] == state:
            return result
        if operation['state'] in ('failed', 'cancelled'):
            raise AssertionError((kind, operation['state'], operation['error_code']))
        time.sleep(.2)
    raise AssertionError('recovery operation timeout')


def snapshot(api, objects):
    listing = api.call('GET', '/books?page_size=100&library_id=' + objects['library']['id'])
    all_ids = [b['id'] for b in listing['items']]
    ids = [b['id'] for b in listing['items'] if b['file_state'] == 'available']
    file_states = {b['id']: b['file_state'] for b in listing['items']}
    assert any(state != 'available' for state in file_states.values()), 'bad archive must remain indexed'
    assert len(all_ids) == listing['total'], 'anonymous fixture must fit one bounded page'
    tags = api.call('GET', '/tag-catalog?library_id=' + objects['library']['id'] + '&category=parody')
    image_digests = {}
    for identity in ids:
        book = api.call('GET', '/books/' + identity)
        body, _ = api.call('GET', '/books/' + identity + '/cover?content_revision=' + str(book['content_revision']), raw=True)
        image_digests['cover:' + identity] = hashlib.sha256(body).hexdigest()
    for tag in tags['items']:
        images = api.call('GET', '/tag-catalog/' + tag['id'] + '/images')
        for role in ('foreground', 'background'):
            if images[role]['asset_id']:
                body, _ = api.call('GET', f'/tag-catalog/{tag["id"]}/images/{role}/content?revision={images[role]["revision"]}', raw=True)
                image_digests[role + ':' + tag['id']] = hashlib.sha256(body).hexdigest()
    return {'editions': {i: api.call('GET', '/books/' + i) for i in all_ids},
            'file_states': file_states,
            'work': api.call('GET', '/works/' + objects['work_id']),
            'series': api.call('GET', '/series/' + objects['series_id'] + '/members'),
            'accounts': api.call('GET', '/accounts'),
            'settings': api.call('GET', '/settings/enhancements'),
            'tags': tags, 'image_digests': image_digests,
            'reading': {i: api.call('GET', '/editions/' + i + '/reading-state') for i in ids}}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=['backup', 'prepare', 'verify', 'verify-write-barrier'])
    p.add_argument('--url', required=True)
    p.add_argument('--evidence', type=Path, required=True)
    p.add_argument('--target-token', type=Path)
    args = p.parse_args()
    os.umask(0o077)
    root = args.evidence
    credentials = json.loads((root / 'credentials.json').read_text())
    objects = json.loads((root / 'objects.json').read_text())
    if args.action == 'backup':
        api = login(args, credentials)
        private_json(root / 'before-backup.json', snapshot(api, objects))
        private_json(root / 'source-session.json', {'cookie': '; '.join(c.name + '=' + c.value for c in api.jar)})
        result = api.call('POST', '/maintenance/backups', {'request_key': 'beta-standard-backup'}, 202)
        wait(api, 'backups', result['id'], 'completed')
        body, _ = api.call('GET', '/maintenance/backups/' + result['id'] + '/download', raw=True)
        (root / 'instance.zip').write_bytes(body)
        private_json(root / 'backup-receipt.json', {'id': result['id'], 'sha256': hashlib.sha256(body).hexdigest(), 'size': len(body)})
        print('PASS authenticated standard backup completed and downloaded')
    elif args.action == 'prepare':
        assert args.target_token
        api = Client(args.url)
        assert api.call('GET', '/auth/status')['initialized'] is False
        target = {'username': 'admin', 'password': secrets.token_urlsafe(26)}
        private_json(root / 'restore-target-credentials.json', target)
        api.call('POST', '/auth/setup', {'setup_token': args.target_token.read_text().strip(), 'password': target['password']})
        private_json(root / 'old-target-session.json', {'cookie': '; '.join(c.name + '=' + c.value for c in api.jar)})
        uploaded = api.call('POST', '/maintenance/restores', (root / 'instance.zip').read_bytes(), 201,
                            headers={'Content-Type': 'application/zip', 'Idempotency-Key': 'beta-standard-restore'})
        identity = uploaded['id']
        api.call('POST', f'/maintenance/restores/{identity}/validate', {'expected_revision': uploaded['revision']}, 202)
        validated = wait(api, 'restores', identity, 'validated')
        preview = api.call('POST', f'/maintenance/restores/{identity}/preview', {
            'expected_revision': validated['operation']['revision'], 'acknowledge_stat_only': True,
            'root_mappings': [{'library_id': objects['library']['id'], 'target_root_key': 'anonymous'}]})
        api.call('POST', f'/maintenance/restores/{identity}/confirm', {
            'expected_revision': preview['operation']['revision'], 'request_key': 'beta-standard-confirm'}, 202)
        private_json(root / 'restore-preview.json', preview)
        print('PASS standard restore validated/mapped/prepared; normal restart required')
    else:
        api = login(args, credentials)
        activation = api.call('GET', '/maintenance/instance-activation')
        state = api.call('GET', '/maintenance/instance-state')
        if args.action == 'verify':
            for filename in ('source-session.json', 'old-target-session.json'):
                stale = json.loads((root / filename).read_text())
                Client(args.url).call('GET', '/auth/session', expected=401, headers={'Cookie': stale['cookie']})
            assert state['background_hold'] and state['write_epoch'] == 0
            time.sleep(2)
            assert api.call('GET', '/maintenance/instance-state')['write_epoch'] == 0
            after = snapshot(api, objects)
            before = json.loads((root / 'before-backup.json').read_text())
            private_json(root / 'after-restore.json', after)
            # Ignore ephemeral pagination snapshots, not business objects.
            for key in ('editions', 'file_states', 'work', 'reading', 'accounts', 'settings', 'image_digests'):
                assert after[key] == before[key], 'restore changed ' + key
            assert after['series']['items'] == before['series']['items']
            assert after['tags']['items'] == before['tags']['items']
            private_json(root / 'activation.json', activation)
            print('PASS restored IDs, fields, Work/Series, accounts, settings, reading and paused background')
        else:
            edition = objects['edition_ids'][0]
            book = api.call('GET', '/books/' + edition)
            api.call('PATCH', '/books/' + edition + '/user-state', {'expected_revision': book['user_state']['revision'], 'hidden': True})
            assert api.call('GET', '/maintenance/instance-state')['write_epoch'] > 0
            identity = json.loads((root / 'restore-preview.json').read_text())['operation']['id']
            api.call('POST', f'/maintenance/restores/{identity}/rollback', {'expected_revision': activation['revision']}, 409)
            print('PASS new personal write blocks unsafe whole-instance rollback')


if __name__ == '__main__':
    main()
