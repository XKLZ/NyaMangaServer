#!/usr/bin/env python3
"""Exercise an explicitly seeded anonymous Shift-JIS ZIP on the beta instance."""
import argparse
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location('beta_api', Path(__file__).with_name('beta-api.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--url', required=True)
p.add_argument('--credentials', type=Path, required=True)
p.add_argument('--evidence', type=Path, required=True)
a = p.parse_args()
a.evidence.mkdir(mode=0o700, parents=True, exist_ok=False)
api = m.Client(a.url)
api.call('POST', '/auth/login', json.loads(a.credentials.read_text()))
libraries = api.call('GET', '/libraries')['items']
assert len(libraries) == 1 and libraries[0]['display_name'] == 'Beta anonymous'
job = api.call('POST', '/libraries/' + libraries[0]['id'] + '/scans', {}, 202)
m.private_json(a.evidence / 'scan-job.json', api.job(job['job_id'], accepted=('completed', 'failed')))
items = api.call('GET', '/jobs/' + job['job_id'] + '/items?page_size=100')['items']
item = next(i for i in items if i['relative_path'] == 'Encoding.cbz')
assert item['error_code'] == 'invalid_archive_encoding', item['error_code']
m.private_json(a.evidence / 'scan-item.json', item)
identity = item['book_id']
preview = api.call('GET', '/editions/' + identity + '/encoding-preview')
m.private_json(a.evidence / 'preview.json', preview)
candidate = next(c for c in preview['candidates'] if c['encoding'] == 'shift_jis')
assert candidate['valid'] and candidate['sample'][0]['display_name'] == 'あ.png'
before = api.call('GET', '/books/' + identity)
expected_parsing = preview['parsing_revision'] + (before['file']['directory_digest'] != candidate['directory_digest'])
command = {k: preview[k] for k in ('content_revision', 'parsing_revision')}
command.update(expected_revision=preview['revision'], encoding='shift_jis', directory_digest=candidate['directory_digest'])
changed = api.call('PUT', '/editions/' + identity + '/encoding', command)
assert changed['content_revision'] == preview['content_revision']
api.call('PUT', '/editions/' + identity + '/encoding', command, 409)
retry = api.call('POST', '/jobs/' + job['job_id'] + '/retry', {'ordinals': [item['ordinal']]}, 202)
m.private_json(a.evidence / 'retry-job.json', api.job(retry['job_id']))
result = api.call('GET', '/books/' + identity)
assert result['file_state'] == 'available' and result['content_revision'] == preview['content_revision']
assert result['file']['parsing_revision'] == expected_parsing
m.private_json(a.evidence / 'result.json', result)
print('PASS explicit Shift-JIS choice, stale CAS rejected, targeted retry and unchanged content revision')
