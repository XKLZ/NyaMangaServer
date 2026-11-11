#!/usr/bin/env python3
"""Anonymous production import gates through the isolated local HTTPS fixture.

Run only after mounting the test CA/proxy and adding Imported.cbz to the
dedicated anonymous volume. No real gallery request is forwarded by the proxy.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import time

spec = importlib.util.spec_from_file_location('beta_api', Path(__file__).with_name('beta-api.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--resume', action='store_true')
    p.add_argument('--url', required=True)
    p.add_argument('--credentials', type=Path, required=True)
    p.add_argument('--evidence', type=Path, required=True)
    a = p.parse_args()
    os.umask(0o077)
    if not a.resume:
        a.evidence.mkdir(mode=0o700, parents=True, exist_ok=False)
    api = module.Client(a.url)
    api.call('POST', '/auth/login', json.loads(a.credentials.read_text()))
    libraries = api.call('GET', '/libraries')['items']
    assert len(libraries) == 1 and libraries[0]['display_name'] == 'Beta anonymous'
    library = libraries[0]['id']
    features = api.call('GET', '/settings/enhancements')['features']
    assert not features['work_recommendations']['enabled'] and not features['series_recommendations']['enabled']
    for endpoint in ('work-discovery-jobs', 'series-discovery-jobs'):
        denied = api.call('POST', '/' + endpoint, {'library_id': library}, 409)
        assert denied['error']['code'] == 'enhancement_disabled'
    settings = api.call('GET', '/settings/metadata-provider')
    assert settings['proxy_url'] == ('http://provider-fixture:18090' if a.resume else '')
    module.private_json(a.evidence / 'fixture-cleanup-required.json', {
        'original_site': settings['site'], 'original_proxy_url': '',
        'required': ['clear metadata proxy through authenticated API',
                     'stop fixture and recreate server with base Compose only']})
    if a.resume:
        batch = json.loads((a.evidence / 'created.json').read_text())
    else:
        api.call('PATCH', '/settings/metadata-provider', {
            'expected_revision': settings['revision'], 'site': 'eh',
            'proxy_url': 'http://provider-fixture:18090'})
        batch = api.call('POST', '/import-batches', {
            'library_id': library, 'request_key': 'beta-local-fixture-import',
            'allow_metadata_network': True}, 202)
        module.private_json(a.evidence / 'created.json', batch)
    identity = batch['id']
    for _ in range(600):
        batch = api.call('GET', '/import-batches/' + identity)
        if batch['state'] in ('failed', 'paused', 'cancelled'):
            module.private_json(a.evidence / 'failed.json', batch)
            raise AssertionError(('import failed', batch['stage'], batch['error_code']))
        if batch['stage'] == 'metadata' and batch['state'] == 'review':
            break
        time.sleep(.2)
    else:
        raise AssertionError('import did not reach metadata review')
    members = api.call('GET', '/import-batches/' + identity + '/members')['items']
    module.private_json(a.evidence / 'members-before.json', members)
    assert len(members) == 8
    member = next(m for m in members if m['filename'] == 'Imported.cbz')
    assert sum(m['status'] == 'file_error' for m in members) == 1
    assert member['candidate_acceptable'] and not member['error_code']
    before = api.call('GET', '/books/' + member['id'])
    assert before['title_original'] != '匿名インポート作品'
    api.call('POST', f'/import-batches/{identity}/control/continue', {'expected_revision': batch['revision']}, 409)
    candidate = api.call('GET', f'/import-batches/{identity}/members/{member["id"]}/candidate')
    module.private_json(a.evidence / 'candidate.json', candidate)
    reviews = [{'id': m['id'], 'expected_revision': m['revision'], 'expected_edition_revision': m['edition_revision'], 'decision': 'skip' if m['status'] == 'file_error' else 'keep'} for m in members if m['id'] != member['id']]
    reviews.append({
        'id': member['id'], 'expected_revision': member['revision'],
        'expected_edition_revision': member['edition_revision'],
        'candidate_revision': candidate['revision'], 'decision': 'accept'})
    api.call('POST', f'/import-batches/{identity}/reviews', {'items': reviews})
    accepted = api.call('GET', '/books/' + member['id'])
    assert accepted['title_original'] == '匿名インポート作品'
    batch = api.call('GET', '/import-batches/' + identity)
    complete = api.call('POST', f'/import-batches/{identity}/control/continue', {'expected_revision': batch['revision']})
    assert complete['state'] == 'completed'
    assert all(complete['enhancement_stages'][stage]['state'] == 'disabled_skipped' for stage in ('versions', 'series'))
    assert not any(job['type'] in ('work_discovery', 'series_discovery') for job in complete['jobs'])
    module.private_json(a.evidence / 'complete.json', complete)
    module.private_json(a.evidence / 'accepted.json', accepted)
    settings = api.call('GET', '/settings/metadata-provider')
    api.call('PATCH', '/settings/metadata-provider', {'expected_revision': settings['revision'], 'proxy_url': ''})
    assert api.call('GET', '/settings/metadata-provider')['proxy_url'] == ''
    module.private_json(a.evidence / 'proxy-cleared.json', {'cleared': True})
    print('PASS isolated metadata candidate, explicit review gate, accepted original title, disabled recommendation stages and proxy removal')


if __name__ == '__main__':
    main()
