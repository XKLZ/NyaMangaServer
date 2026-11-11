"""Explicit verified offline cache transfer for the isolated validation snapshot.
Never performs inference; never imports experimental code or trusts original absolute paths.
"""
import argparse
import fcntl
import os
import time
import json
from pathlib import Path
from common import atomic_json, digest, file_digest, no_symlinks, private_dir
from features import Runtime, VERSIONS, vector, rss_bytes


def snapshot_record_matches(typed, original):
    """Go's declared string view maps only a nullable original title to empty.

    The original whitelist bytes remain authoritative via the manifest/file SHA.
    No other missing, null or changed field is normalized or ignored.
    """
    if not isinstance(typed, dict) or not isinstance(original, dict):
        return False
    normalized = dict(original)
    if 'title_jpn' in normalized and normalized['title_jpn'] is None:
        normalized['title_jpn'] = ''
    return typed == normalized


def transfer_item(algorithm, index, book, raw, old_roots, output, model_sha):
    text = (book.get('title_jpn') or book.get('title') or '').strip()[:8192]
    input_sha = digest(text) if algorithm == 'minilm' else __import__('hashlib').sha256(raw).hexdigest() if raw is not None else None
    if input_sha is None:
        return 'missing_cover'
    expected = digest([VERSIONS[algorithm], model_sha, input_sha])
    entry = index.get('items', {}).get(str(book['id']), {})
    if index.get('version') != VERSIONS[algorithm] or index.get('model_sha256') != model_sha or entry.get('status') != 'ok' or entry.get('fingerprint') != expected:
        return 'input_or_version_mismatch'
    item = None
    for root in old_roots:
        path = no_symlinks(root / algorithm / (expected + '.json'))
        if path.is_file() and path.stat().st_size <= 65536:
            candidate = json.loads(path.read_text())
            if (candidate.get('fingerprint') == expected and candidate.get('input_sha256') == input_sha
                and candidate.get('model_sha256') == model_sha and candidate.get('algorithm_version') == VERSIONS[algorithm]
                and candidate.get('feature_digest') == digest(candidate.get('feature'))
                and digest(entry.get('feature')) == digest(candidate.get('feature'))):
                item = candidate
                break
    if item is None:
        return 'unverified_cache'
    if algorithm == 'phash':
        if not isinstance(item['feature'], str) or len(item['feature']) != 16:
            return 'invalid_feature'
        int(item['feature'], 16)
    else:
        vector(item['feature'], algorithm)
    path = no_symlinks(output / algorithm / (expected + '.json'))
    if path.exists():
        if json.loads(path.read_text()) != item:
            raise ValueError('destination_cache_conflict')
        return 'already_present'
    atomic_json(path, item)
    return 'reused'


def run(repo):
    repo = no_symlinks(repo)
    data = no_symlinks(repo / ".runtime/tests/runs/work-recommendation-pilot/instance/data")
    lock = no_symlinks(data / "app.lock")
    fd = os.open(lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return transfer_snapshot(repo)
    finally:
        os.close(fd)


def transfer_snapshot(repo):
    started = time.monotonic()
    repo = no_symlinks(repo)
    base = no_symlinks(repo / '.runtime/tests/runs/work-recommendation-pilot/instance')
    report = json.loads(no_symlinks(base / 'reports/import.json').read_text())
    source = no_symlinks(repo / '.runtime/tests/runs/recommendations/run-v3/analysis/evaluation')
    books_path = no_symlinks(source / 'books.json')
    if report['version'] != 1 or file_digest(books_path) != report['input_sha256']:
        raise ValueError('snapshot_input_changed')
    manifest = json.loads(no_symlinks(source / 'input-manifest.json').read_text())
    books = json.loads(books_path.read_text())['books']
    records = {r['source_id']: r for r in report['records']}
    if len(records) != len(books):
        raise ValueError('snapshot_mapping_incomplete')
    output = private_dir(base / 'data/indexes/recommendations/features')
    config = json.loads(no_symlinks(base / 'config.json').read_text())
    if config['listen_addr'] != '127.0.0.1:8081' or config['public_url'] != 'http://127.0.0.1:5174' or config['session_cookie_name'] != 'nyamanga_recommendation_pilot':
        raise ValueError('wrong_pilot_instance')
    runtime = Runtime(no_symlinks(config['work_recommendations']['models_dir']), output)
    # Artifact SHA verification only: no Runtime.load/session/feature invocation.
    models = {a: runtime.model_identity(a) for a in VERSIONS}
    old_roots = [no_symlinks(repo / f'.runtime/tests/runs/recommendations/run-v{v}/feature-cache') for v in (1, 2)]
    indexes = {}
    for algorithm in VERSIONS:
        name = f'features-{algorithm}.json'
        path = no_symlinks(source / name)
        expected = manifest['files'].get(name)
        if isinstance(expected, dict):
            expected = expected.get('sha256')
        if file_digest(path) != expected:
            raise ValueError('feature_index_checksum_mismatch')
        indexes[algorithm] = json.loads(path.read_text())
    counts = {a: {} for a in VERSIONS}
    null_original_titles = 0
    for book in books:
        record = records[str(book['id'])]
        if not snapshot_record_matches(record['legacy_unknown'], book):
            raise ValueError('snapshot_record_mismatch')
        if book.get('title_jpn') is None and 'title_jpn' in book:
            null_original_titles += 1
        raw = None
        if record.get('asset'):
            asset = record['asset']
            key = Path(asset['Key'] if 'Key' in asset else asset['storage_key'])
            if key.is_absolute() or '..' in key.parts or key.parts[:2] != ('assets', 'recommendation-validation'):
                raise ValueError('asset_boundary')
            path = no_symlinks(base / 'data' / key)
            if path.stat().st_size > 10 * 1024**2:
                raise ValueError("asset_byte_limit")
            raw = path.read_bytes()
            expected = asset.get('Digest', asset.get('digest'))
            if len(raw) > 10 * 1024**2 or file_digest(path) != expected:
                raise ValueError('asset_checksum_mismatch')
        for algorithm in VERSIONS:
            state = transfer_item(algorithm, indexes[algorithm], book, raw, old_roots, output, models[algorithm])
            counts[algorithm][state] = counts[algorithm].get(state, 0) + 1
    result = {'version': 1, 'input_sha256': report['input_sha256'], 'models': models, 'counts': counts,
              'computed_inputs': 0, 'model_loads': 0, 'feature_versions': VERSIONS,
              'legacy_typed_view_normalizations': {'title_jpn_null_to_empty': null_original_titles},
              'original_bytes_preserved_in_whitelist_via_input_sha256': True,
              'elapsed_seconds': time.monotonic() - started, 'peak_rss_bytes': rss_bytes(),
              'validation': 'manifest_actual_imported_cover_or_original_title_model_preprocessor_feature_digest', 'notice': 'Verified reuse only; unavailable features await explicit user-triggered jobs.'}
    atomic_json(base / 'reports/cache-transfer.json', result)
    print(json.dumps({'counts': counts, 'computed_inputs': 0, 'model_loads': 0}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--root', required=True, type=Path)
    run(parser.parse_args().root)
