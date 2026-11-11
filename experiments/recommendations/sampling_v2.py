"""Fresh four-relation annotation study; preparation only, no quality evaluation."""
from __future__ import annotations
import collections
import itertools
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from types import SimpleNamespace
from core import (INPUT, REPO, RUNTIME, RULE_VERSION, atomic_json, checkpoint, digest,
                  families, file_digest, private_output, read_source, reject_symlinks,
                  book_structure, rules)

SCHEMA = 'recommendation-pairs-v2'
LABEL_SCHEMA = 'recommendation-labels-v2'
LABELS = (None, 'same_work', 'same_series', 'unrelated', 'uncertain')
OLD = RUNTIME / 'run-v1'


def read_json(path):
    reject_symlinks(path)
    return json.loads(path.read_text())


def original_fingerprints():
    return {name: file_digest(OLD / name) for name in
            ('candidates.json', 'review.html', 'labels.user.json')}


def initialize(output):
    if (output / 'candidates.json').exists():
        raise ValueError('v2_candidate_pack_exists_no_overwrite')
    books, source = read_source()
    original = original_fingerprints()
    preparation = {'schema': 'recommendation-preparation-v2', 'source': source,
                   'old_artifacts': original, 'feature_book_ids': sorted(books)}
    preparation['pack_id'] = digest(preparation)
    path = output / 'feature-input.json'
    if path.exists():
        if read_json(path) != preparation:
            raise ValueError('resumed_preparation_inputs_changed')
    else:
        atomic_json(output / 'books.json', {'source': source, 'books': list(books.values())})
        atomic_json(path, preparation)
    return preparation, books


def feature_material(output):
    preparation = read_json(output / 'feature-input.json')
    exported = read_json(output / 'books.json')
    if exported['source'] != preparation['source']:
        raise ValueError('preparation_source_mismatch')
    books = {b['id']: b for b in exported['books']}
    return preparation, books, preparation['feature_book_ids']


def compute(output, algorithm):
    from features import run
    run(output, SimpleNamespace(algorithm=algorithm, limit=0),
        material=feature_material(output), reuse_from=(OLD,), checkpoint_every=50)


def validate_labels(pack, labels):
    if not isinstance(labels, dict) or labels.get('schema') != LABEL_SCHEMA or labels.get('pack_id') != pack['pack_id']:
        raise ValueError('v2_annotation_pack_or_schema_mismatch')
    rows = labels.get('annotations')
    if not isinstance(rows, list) or len(rows) != len(pack['pairs']):
        raise ValueError('v2_export_must_include_every_pair')
    valid = {p['pair_id'] for p in pack['pairs']}
    seen = set()
    for row in rows:
        if not isinstance(row, dict) or row.get('pair_id') not in valid or row['pair_id'] in seen:
            raise ValueError('v2_unknown_or_duplicate_pair')
        if row.get('label') not in LABELS or not isinstance(row.get('reason'), str) or len(row['reason']) > 4000:
            raise ValueError('v2_invalid_label_or_reason')
        if type(row.get('collection_or_partial_overlap')) is not bool:
            raise ValueError('v2_invalid_overlap_flag')
        seen.add(row['pair_id'])
    return labels


def prepare(output):
    initialize(output)
    for algorithm in ('phash', 'sscd', 'minilm'):
        checkpoint()
        process = subprocess.Popen([sys.executable, str(REPO / 'experiments/recommendations/study.py'),
                                    'prepare-v2-features', '--output', str(output), '--algorithm', algorithm])
        try:
            while process.poll() is None:
                checkpoint()
                time.sleep(.1)
            checkpoint()
            if process.returncode:
                raise RuntimeError('feature_preparation_failed:' + algorithm)
        except BaseException:
            process.terminate()
            try: process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill();process.wait()
            raise
    finalize(output)


def neighbors(output, preparation, books, k=20):
    """Fixed models retrieve candidates only; no labels, calibration or ranking study."""
    from models import offline_environment
    offline_environment()
    import numpy as np
    result = {}
    for algorithm in ('phash', 'sscd', 'minilm'):
        material = read_json(output / ('features-' + algorithm + '.json'))
        from features import FEATURE_VERSIONS
        if material['pack_id'] != preparation['pack_id'] or material['version'] != FEATURE_VERSIONS[algorithm]:
            raise ValueError('v2_feature_input_changed')
        items = material['items']
        ids = sorted(int(i) for i, row in items.items() if row['status'] == 'ok')
        if algorithm == 'phash':
            vectors = np.asarray([int(items[str(i)]['feature'], 16) for i in ids], dtype=np.uint64)
        else:
            vectors = np.asarray([items[str(i)]['feature'] for i in ids], dtype=np.float32)
        scores = {}
        count = min(k, len(ids) - 1)
        if count <= 0:
            raise ValueError('not_enough_valid_feature_inputs')
        for offset in range(0, len(ids), 32):
            checkpoint()
            if algorithm == 'phash':
                block = 1 - np.bitwise_count(vectors[offset:offset + 32, None] ^ vectors[None, :]).astype(np.float32) / 63
            else:
                block = vectors[offset:offset + 32] @ vectors.T
            for j, row in enumerate(block):
                row[offset + j] = -np.inf
                # Stable tie breaking is important for identical covers/titles.
                top = np.lexsort((np.asarray(ids), -row))[:count]
                a = ids[offset + j]
                for index in top:
                    b = ids[int(index)];pair = tuple(sorted((a, b)))
                    scores[pair] = max(scores.get(pair, -1), float(row[index]))
        result[algorithm] = scores
    return result


def build_pools(books, routes):
    structures = {i: book_structure(b) for i, b in books.items()}
    creators = {i: {tuple(t) for t in b['tags'] if t[0] in ('artist', 'group')} for i, b in books.items()}
    pools = {name: {} for name in ('work_existing', 'work_routes', 'series_existing', 'series_routes', 'hard', 'ordinary')}
    def add(pool, route, pair, score=0):
        if pair[0] != pair[1]:
            pools[pool].setdefault(route, {})[tuple(sorted(pair))] = float(score)
    def same_group(a, b):
        return books[a]['version_group_id'] is not None and books[a]['version_group_id'] == books[b]['version_group_id']
    def same_series(a, b):
        return books[a]['collection_id'] is not None and books[a]['collection_id'] == books[b]['collection_id']
    def measures(a, b):
        sa, sb = structures[a], structures[b]
        union = sa['grams'] | sb['grams']
        title = len(sa['grams'] & sb['grams']) / len(union) if union else 0
        conflict = bool(sa['volumes'] and sb['volumes'] and sa['volumes'] != sb['volumes'])
        return title, conflict, bool(creators[a] & creators[b])
    for pool, field in (('work_existing', 'version_group_id'), ('series_existing', 'collection_id')):
        groups = collections.defaultdict(list)
        for i, b in books.items():
            if b[field] is None: continue
            if pool == 'series_existing' and b['version_group_id'] is not None and not b['is_primary_version']: continue
            groups[b[field]].append(i)
        for ids in groups.values():
            for a, b in itertools.combinations(sorted(ids), 2):
                if pool == 'series_existing' and same_group(a, b): continue
                add(pool, 'organization_hint', (a, b))
    # Title/creator candidates are independent of model nearest-neighbor routes.
    grams = collections.defaultdict(set);by_creator = collections.defaultdict(set)
    for i in books:
        for gram in structures[i]['grams']: grams[gram].add(i)
        for creator in creators[i]: by_creator[creator].add(i)
    title_pairs = {}
    for a in books:
        checkpoint()
        nearby = collections.Counter()
        for gram in structures[a]['grams']:
            if len(grams[gram]) <= 250: nearby.update(grams[gram])
        for creator in creators[a]:
            if len(by_creator[creator]) <= 300: nearby.update(by_creator[creator])
        ranked = []
        for b in nearby:
            if a == b: continue
            title, conflict, shared = measures(a, b)
            ranked.append((title + .1 * shared, b))
        for score, b in sorted(ranked, key=lambda x: (-x[0], x[1]))[:20]:
            title_pairs[tuple(sorted((a, b)))] = score
    for route, pairs in {'title_creator': title_pairs, **routes}.items():
        for (a, b), score in pairs.items():
            title, conflict, shared = measures(a, b)
            if route != 'minilm' and not same_group(a, b) and not conflict:
                add('work_routes', route, (a, b), score)
            if route in ('title_creator', 'minilm') and not same_group(a, b) and not same_series(a, b):
                add('series_routes', route, (a, b), score + .1 * conflict)
            if same_group(a, b) or same_series(a, b): continue
            if conflict and shared:
                add('hard', 'same_creator_different_ordinal', (a, b), title)
            if route in ('sscd', 'phash') and title < .65:
                add('hard', 'cover_close_title_different', (a, b), score)
            if route == 'minilm' and title < .5:
                add('hard', 'semantic_close_different_title_or_series', (a, b), score)
            if shared and title < .65:
                add('hard', 'same_creator_different_work_hint', (a, b), .3 - abs(title - .3))
    galleries = collections.defaultdict(list)
    for i, b in books.items():
        if b.get('gallery_id_sampling_only') is not None: galleries[b['gallery_id_sampling_only']].append(i)
    for ids in galleries.values():
        for a, b in itertools.combinations(sorted(ids), 2):
            if not same_group(a, b) and not same_series(a, b):
                add('hard', 'same_gallery_different_organization', (a, b))
    ids = sorted(books, key=lambda i: digest(['ordinary-1729', i]))
    for offset in (1, 7, 31, 103):
        for index, a in enumerate(ids):
            b = ids[(index + offset) % len(ids)]
            title, conflict, shared = measures(a, b)
            if a != b and title < .08 and not shared and not same_group(a, b) and not same_series(a, b):
                add('ordinary', 'dissimilar_title_creator', (a, b))
    return pools


def select(books, old, pools):
    family, splits = families(books)
    old_books = {i for p in old['pairs'] for i in p['book_ids']}
    reviewed_families = {family[i] for i in old_books if i in family}
    for i in books:
        if family[i] in reviewed_families: splits[i] = 'calibration'
    excluded = {tuple(sorted(p['book_ids'])) for p in old['pairs']}
    selected = [];used = set(excluded);usage = collections.Counter();fallbacks = []
    quotas = [('work_existing', 50), ('series_existing', 50), ('work_routes', 50),
              ('series_routes', 50), ('hard', 80), ('ordinary', 20)]
    for pool, count in quotas:
        for split, quota in (('calibration', count * 7 // 10), ('test', count * 3 // 10)):
            route_names = sorted(pools[pool])
            lists = {}
            for route in route_names:
                pairs = [(pair, score) for pair, score in pools[pool][route].items()
                         if splits[pair[0]] == splits[pair[1]] == split]
                pairs.sort(key=lambda x: (-x[1], digest([pool, route, *x[0]])))
                lists[route] = pairs
            taken = 0;counts = collections.Counter()
            # Round-robin routes rather than one global rule-score ranking.
            for cap in (4, 6, 8, 12):
                positions = {route: 0 for route in route_names}
                progress = True
                while taken < quota and progress:
                    progress = False
                    for route in route_names:
                        pairs = lists[route]
                        while positions[route] < len(pairs):
                            pair, score = pairs[positions[route]];positions[route] += 1
                            fam = {family[i] for i in pair}
                            if pair in used or any(usage[f] >= cap for f in fam): continue
                            used.add(pair);usage.update(fam);counts[route] += 1;taken += 1;progress = True
                            selected.append({'pair_id': 'pair-' + digest(['relations-v2', *pair])[:20],
                                             'book_ids': list(pair), 'sampling_pool': pool, 'sampling_route': route,
                                             'sampling_score': score, 'split': split, 'family_ids': sorted(fam),
                                             'family_cap_at_selection': cap})
                            break
                        if taken == quota: break
                if taken == quota: break
            if taken != quota:
                raise ValueError('insufficient_v2_pool:' + pool + ':' + split + ':' + str(taken) + '/' + str(quota))
            fallbacks.append({'pool': pool, 'split': split, 'selected': taken, 'routes': dict(counts),
                              'family_cap': cap, 'route_reallocation': 'round_robin_available_routes_fixed_lexical_order'})
    selected.sort(key=lambda p: digest(['display-shuffle-1729', p['pair_id']]))
    for index, pair in enumerate(selected): pair['batch'] = index // 30 + 1
    calibration = {f for p in selected if p['split'] == 'calibration' for f in p['family_ids']}
    test = {f for p in selected if p['split'] == 'test' for f in p['family_ids']}
    assert len(selected) == 300 and not calibration & test and not test & reviewed_families
    assert not excluded & {tuple(p['book_ids']) for p in selected}
    return selected, {'known_family_overlap': 0, 'reviewed_family_test_overlap': 0,
                      'excluded_old_unique_pairs': len(excluded), 'split_counts': dict(collections.Counter(p['split'] for p in selected)),
                      'max_family_pair_appearances': max(usage.values()), 'selection_details': fallbacks,
                      'unknown_semantic_family_overlap': 'human_audit_required_before_final_freeze',
                      'family_algorithm': RULE_VERSION + '+organization-links', 'seed': 1729}


def finalize(output):
    from features import worker_lock
    with worker_lock(output):
        return _finalize(output)


def _finalize(output):
    from review_v2 import render
    if (output / 'candidates.json').exists():
        raise ValueError('v2_candidate_pack_exists_no_overwrite')
    started = time.monotonic()
    preparation, books, ids = feature_material(output)
    if original_fingerprints() != preparation['old_artifacts']:
        raise ValueError('original_pilot_changed_during_preparation')
    for algorithm in ('phash', 'sscd', 'minilm'):
        path = output / ('resources-' + algorithm + '.json')
        resource = read_json(path)
        if resource['pack_id'] != preparation['pack_id']:
            raise ValueError('preparation_resource_generation_mismatch')
        resource['scope'] = 'source_copy_feature_preparation_only'
        atomic_json(path, resource)
    routes = neighbors(output, preparation, books)
    pools = build_pools(books, routes)
    pairs, isolation = select(books, read_json(OLD / 'candidates.json'), pools)
    pack = {'schema': SCHEMA, 'source': preparation['source'], 'feature_input_id': preparation['pack_id'],
            'old_artifacts': preparation['old_artifacts'], 'isolation': isolation, 'pairs': pairs,
            'purpose': 'annotation_preparation_only_not_ground_truth_or_quality_evaluation'}
    selected_ids = {i for p in pairs for i in p['book_ids']}
    from core import cover_file
    cover_inputs = {}
    feature_items = read_json(output / 'features-sscd.json')['items']
    for i in sorted(selected_ids):
        row = feature_items[str(i)]
        if row['status'] != 'ok':
            cover_inputs[str(i)] = None
            continue
        cached = read_json(output / 'feature-cache/sscd' / (row['fingerprint'] + '.json'))
        actual = file_digest(cover_file(INPUT, books[i]['cover_path']))
        if actual != cached['input_sha256']:
            raise ValueError('selected_cover_changed_after_feature_computation')
        cover_inputs[str(i)] = actual
    pack['selected_cover_sha256'] = cover_inputs
    pack['pack_id'] = digest(pack)
    empty = {'schema': LABEL_SCHEMA, 'pack_id': pack['pack_id'], 'annotations': [
        {'pair_id': p['pair_id'], 'label': None, 'reason': '', 'collection_or_partial_overlap': False} for p in pairs]}
    validate_labels(pack, empty)
    images = render(pack, books, INPUT, output / 'review.html')
    atomic_json(output / 'labels-input.json', empty)
    atomic_json(output / 'preparation-summary.json', {'pairs': len(pairs), 'batches': 10, 'batch_size': 30,
                'pool_counts': dict(collections.Counter(p['sampling_pool'] for p in pairs)),
                'isolation': isolation, 'duration_seconds': time.monotonic() - started, **images})
    # Publish the immutable pack only after the review material is complete.
    atomic_json(output / 'candidates.json', pack)
    if original_fingerprints() != preparation['old_artifacts']:
        raise ValueError('original_pilot_changed')
    print(json.dumps({'status': 'ready_for_user_annotations', 'pairs': 300, 'batches': 10,
                      'duration_seconds': time.monotonic() - started, 'isolation': isolation, **images}), flush=True)
