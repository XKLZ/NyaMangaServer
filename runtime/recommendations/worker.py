#!/usr/bin/env python3
"""Local JSON-lines worker. Offline, one request at a time, no source paths in input."""
import argparse
import base64
import json
import os
import sys
import time
from pathlib import Path

os.environ['PYTHONDONTWRITEBYTECODE'] = '1'
from features import Runtime, VERSIONS, vector, rss_bytes, check_resources
import matching
import labels
from discovery import Discovery
from series import SeriesDiscovery

PROTOCOL = 'work-recommendation-worker-v1'
MAX_MESSAGE = 32 * 1024**2
MAX_BOOKS = 50000


def validate_book(book):
    if not isinstance(book, dict) or not isinstance(book.get('id'), str) or not 1 <= len(book['id']) <= 128:
        raise ValueError('invalid_edition')
    for key in ('title', 'title_jpn'):
        if book.get(key) is not None and (not isinstance(book[key], str) or len(book[key]) > 8192):
            raise ValueError('invalid_title')
    tags = book.get('tags', [])
    if not isinstance(tags, list) or len(tags) > 2000 or any(not isinstance(v, list) or len(v) != 2 or any(not isinstance(x, str) or len(x) > 256 for x in v) for v in tags):
        raise ValueError('invalid_tags')
    if not isinstance(book.get('pages', 0), int) or not 0 <= book.get('pages', 0) <= 10000:
        raise ValueError('invalid_page_count')


def recommend(request):
    items = request['books']
    if not isinstance(items, list) or not 1 <= len(items) <= MAX_BOOKS:
        raise ValueError('book_limit')
    for b in items:
        validate_book(b)
    books = {b['id']: b for b in items}
    if len(books) != len(items):
        raise ValueError('duplicate_edition')
    query = request['query_edition_id']
    if query not in books:
        raise ValueError('query_missing')
    material = request.get('features', {})
    features = {a: {} for a in VERSIONS}
    for algorithm, rows in material.items():
        if algorithm not in VERSIONS or not isinstance(rows, dict):
            raise ValueError('invalid_feature_algorithm')
        for i, value in rows.items():
            if i not in books:
                raise ValueError('feature_edition_missing')
            if algorithm == 'phash':
                if not isinstance(value, str) or len(value) != 16:
                    raise ValueError('invalid_phash')
                features[algorithm][i] = int(value, 16)
            else:
                if isinstance(value, dict):
                    if set(value) != {'encoding', 'data'} or value['encoding'] != 'f32le':
                        raise ValueError('invalid_feature_encoding')
                    import numpy as np
                    value = np.frombuffer(base64.b64decode(value['data'], validate=True), dtype='<f4')
                features[algorithm][i] = vector(value, algorithm)
    parsed = matching.structures(books)
    candidates = []
    seen = set()
    for work in request['works']:
        if not isinstance(work, dict) or not isinstance(work.get('id'), str) or work['id'] in seen:
            raise ValueError('duplicate_or_invalid_work')
        seen.add(work['id'])
        members = work['edition_ids']
        if not isinstance(members, list) or not 1 <= len(members) <= 100 or len(set(members)) != len(members) or any(i not in books for i in members) or work['preferred_edition_id'] not in members:
            raise ValueError('invalid_work_members')
        if query in members:
            continue
        # Library boundaries are enforced again even though the Go repository filters.
        if any(books[i].get('library_id') != books[query].get('library_id') for i in members):
            raise ValueError('cross_library_candidate')
        signals = {i: matching.pair_signal(query, i, books, parsed, features, matching.V2_PARAMETERS) for i in members}
        candidate = matching.aggregate_work(query, work, signals, matching.PARAMETERS)
        if candidate['tier'] == 'none':
            continue
        # Labels are hypothetical and never written or used as matching evidence.
        candidate['label_suggestions'] = labels.suggest_work([books[query], *(books[i] for i in members)])
        candidates.append(candidate)
    ranked = matching.rank_works(candidates, 20)
    check_resources()
    return {'algorithm_version': matching.VERSION, 'parameters': matching.PARAMETERS, 'items': ranked, 'candidate_count': len(candidates), 'truncated': len(candidates) > 20, 'max_results': 20, 'score_semantics': 'matching_signal_not_probability', 'automatic_merge': False}


def dispatch(runtime, request):
    if not isinstance(request, dict) or request.get('protocol') != PROTOCOL:
        raise ValueError('protocol_mismatch')
    operation = request.get('operation')
    if operation == 'capabilities':
        return runtime.capabilities()
    if operation == 'cached_features':
        result={}
        references=request.get('features',{})
        if not isinstance(references,dict) or set(references)-set(VERSIONS):raise ValueError('invalid_feature_references')
        for algorithm,reference in references.items():
            item=runtime.cached_feature(algorithm,reference.get('fingerprint'))
            if item is None or item.get('feature_digest')!=reference.get('feature_digest'):return {'valid':False}
            result[algorithm]=item
        return {'valid':True,'result':{'features':result,'missing_cover':bool(request.get('missing_cover'))}}
    if operation == 'features':
        book = request['book']
        validate_book(book)
        result = {}
        image = request.get('image_base64')
        if image is not None:
            if not isinstance(image, str) or len(image) > 14 * 1024**2:
                raise ValueError('image_byte_limit')
            raw = base64.b64decode(image, validate=True)
            for algorithm in ('phash', 'sscd'):
                result[algorithm] = runtime.feature(algorithm, raw, repair=True) if request.get('repair_cache') else runtime.feature(algorithm, raw)
        text = (book.get('title_jpn') or book.get('title') or '').strip()[:8192]
        if text:
            result['minilm'] = runtime.feature('minilm', text, repair=True) if request.get('repair_cache') else runtime.feature('minilm', text)
        return {'features': result, 'missing_cover': image is None}
    if operation == 'series_features':
        book=request['book'];validate_book(book)
        text=(book.get('title_jpn') or book.get('title') or '').strip()[:8192]
        return {'features':{'minilm':runtime.feature('minilm',text)} if text else {}}
    if operation == 'series_prepare':
        runtime.series=SeriesDiscovery(request,validate_book,getattr(runtime,'series',None))
        return {'pool_key':runtime.series.key,'works':len(runtime.series.corpus.ids),'profiles':runtime.series.profiles}
    if operation in ('series_row','series_cluster','series_validate','series_restore','series_checkpoint'):
        if not hasattr(runtime,'series'):raise ValueError('series_pool_missing')
        return getattr(runtime.series,operation.removeprefix('series_'))(request)
    if operation == 'discovery_preprocessing':
        from common import digest
        books={b['id']:b for b in request['books']}
        for b in books.values():validate_book(b)
        def canonical(v):
            if isinstance(v,dict):return {k:canonical(x) for k,x in v.items()}
            if isinstance(v,set):return sorted(canonical(x) for x in v)
            if isinstance(v,(list,tuple)):return [canonical(x) for x in v]
            return v
        return {'fingerprints':{i:digest(canonical(v)) for i,v in matching.structures(books).items()},'version':'work-parsed-views-v1'}
    if operation == 'discovery_prepare':
        runtime.discovery = Discovery(request, validate_book)
        return {'pool_key': runtime.discovery.key, 'books': len(runtime.discovery.ids), 'titles_parsed': len(runtime.discovery.ids)}
    if operation == 'discovery_row':
        if not hasattr(runtime, 'discovery'): raise ValueError('discovery_pool_missing')
        return runtime.discovery.row(request)
    if operation == 'recommend':
        return recommend(request)
    raise ValueError('unknown_operation')


def run():
    parser = argparse.ArgumentParser()
    parser.add_argument('--models', required=True)
    parser.add_argument('--cache', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    runtime = Runtime(Path(args.models), Path(args.cache))
    while True:
        line = sys.stdin.buffer.readline(MAX_MESSAGE + 1)
        if not line:
            return
        if len(line) > MAX_MESSAGE or not line.endswith(b'\n'):
            # Do not attempt to resynchronize a truncated/oversized private message.
            return
        started = time.monotonic()
        try:
            request = json.loads(line)
            result = dispatch(runtime, request)
            reply = {'protocol': PROTOCOL, 'result': result, 'resources': {**runtime.stats, 'operation_seconds': time.monotonic() - started, 'peak_rss_bytes': rss_bytes(), 'nas_performance_verified': False}}
        except Exception as error:
            # Paths, titles, model runtime internals and input bytes never enter logs.
            code = 'worker_resource_budget_exceeded' if isinstance(error, MemoryError) else 'recommendation_worker_failed'
            if isinstance(error, ValueError) and error.args in (('invalid_series_checkpoint',), ('series_restore_limit',), ('invalid_series_signal',)):
                code = 'invalid_series_checkpoint'
            if isinstance(error, ValueError) and error.args in (('discovery_pool_missing',), ('discovery_pool_changed',), ('series_pool_missing',), ('series_pool_changed',)):
                code = error.args[0]
            reply = {'protocol': PROTOCOL, 'error': code, 'resources': {**runtime.stats, 'operation_seconds': time.monotonic() - started, 'peak_rss_bytes': rss_bytes(), 'nas_performance_verified': False}}
        raw = json.dumps(reply, ensure_ascii=False, separators=(',', ':'), allow_nan=False).encode() + b'\n'
        if len(raw) > MAX_MESSAGE:
            raw = b'{"protocol":"work-recommendation-worker-v1","error":"response_too_large"}\n'
        sys.stdout.buffer.write(raw)
        sys.stdout.buffer.flush()


if __name__ == '__main__':
    run()
