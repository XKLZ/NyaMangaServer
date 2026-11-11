"""Prepare a genuinely new, blind heldout booklet; never read annotation answers."""
from __future__ import annotations
import collections
import json
import time
from core import (INPUT, RUNTIME, RULE_VERSION, atomic_json, checkpoint, cover_file,
                  digest, families, file_digest)
from sampling_v2 import (LABEL_SCHEMA, SCHEMA, build_pools, feature_material,
                         read_json, validate_labels)

PREVIOUS = RUNTIME / 'run-v2'
VERSION = 'heldout-sampling-v3-fixed-v2-routes-1729'
V2_APPROVED_SHA256 = '45e27a79a7d945e7d830d6b557156b5b914a550e0a6066ed1e5f9025d15caeb9'
QUOTAS = (('work_existing', 20), ('series_existing', 20), ('work_routes', 20),
          ('series_routes', 20), ('hard', 30), ('ordinary', 10))


def verify_v2_freeze():
    from analyze_v2 import code_hashes, input_hashes
    directory = PREVIOUS / 'analysis'
    path = directory / 'frozen-policy.json'
    if file_digest(path) != V2_APPROVED_SHA256:
        raise ValueError('previous_approved_policy_changed')
    frozen = read_json(path)
    if frozen['code_sha256'] != code_hashes() or frozen['input_sha256'] != input_hashes(directory):
        raise ValueError('previous_frozen_code_or_inputs_changed')


def protected_artifacts():
    paths = [RUNTIME / 'run-v1' / name for name in
             ('candidates.json', 'review.html', 'labels.user.json')]
    paths += [PREVIOUS / name for name in
              ('candidates.json', 'review.html', 'labels.user.v2.json', 'books.json',
               'feature-input.json', 'features-phash.json', 'features-sscd.json', 'features-minilm.json')]
    paths += sorted(p for p in (PREVIOUS / 'analysis').iterdir() if p.is_file())
    return {str(p.relative_to(RUNTIME)): file_digest(p) for p in paths}


def cached_cover_hashes(books):
    """Original bytes, not cover filename hashes or perceptual similarity."""
    items = read_json(PREVIOUS / 'features-sscd.json')['items']
    result = {}
    for i in sorted(books):
        checkpoint()
        row = items[str(i)]
        if row['status'] != 'ok':
            result[i] = None
            continue
        cached = read_json(PREVIOUS / 'feature-cache/sscd' / (row['fingerprint'] + '.json'))
        value = cached['input_sha256']
        if len(value) != 64 or any(c not in '0123456789abcdef' for c in value):
            raise ValueError('invalid_cached_cover_digest')
        result[i] = value
    return result


def eligible_books(books, prior_packs, cover_hashes):
    family, _ = families(books)
    reviewed = {i for pack in prior_packs for p in pack['pairs'] for i in p['book_ids']}
    if not reviewed <= books.keys():
        raise ValueError('reviewed_book_missing_from_frozen_source')
    reviewed_families = {family[i] for i in reviewed}
    reviewed_covers = {cover_hashes[i] for i in reviewed if cover_hashes[i] is not None}
    cover_collisions = {i for i in books if cover_hashes[i] in reviewed_covers}
    # Propagate exact-cover exclusions to their known families as well.
    excluded_families = reviewed_families | {family[i] for i in cover_collisions}
    eligible = {i: b for i, b in books.items() if family[i] not in excluded_families}
    audit = {'reviewed_unique_books': len(reviewed), 'reviewed_known_families': len(reviewed_families),
             'reviewed_unique_cover_bytes': len(reviewed_covers),
             'additional_exact_cover_books_outside_reviewed_families': sum(family[i] not in reviewed_families for i in cover_collisions),
             'excluded_known_families': len(excluded_families), 'eligible_books': len(eligible),
             'eligible_known_families': len({family[i] for i in eligible}),
             'family_algorithm': RULE_VERSION + '+organization-links',
             'unknown_semantic_family_overlap': 'human_audit_before_algorithm_freeze_required'}
    return eligible, family, audit


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
        ids = sorted(int(i) for i, row in items.items() if row['status'] == 'ok' and int(i) in books)
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


def select(books, family, pools):
    selected = [];used = set();usage = collections.Counter();details = []
    for pool, quota in QUOTAS:
        route_names = sorted(pools[pool]);lists = {}
        for route in route_names:
            lists[route] = sorted(pools[pool][route].items(),
                                 key=lambda x: (-x[1], digest([pool, route, *x[0]])))
        taken = 0;counts = collections.Counter()
        for cap in (4, 6, 8, 12):
            positions = {route: 0 for route in route_names};progress = True
            while taken < quota and progress:
                progress = False
                for route in route_names:
                    pairs = lists[route]
                    while positions[route] < len(pairs):
                        checkpoint()
                        pair, score = pairs[positions[route]];positions[route] += 1
                        if any(i not in books for i in pair):
                            raise ValueError('candidate_outside_eligible_books')
                        fam = {family[i] for i in pair}
                        if pair in used or any(usage[f] >= cap for f in fam): continue
                        used.add(pair);usage.update(fam);counts[route] += 1;taken += 1;progress = True
                        selected.append({'pair_id': 'pair-' + digest(['relations-v3', *pair])[:20],
                                         'book_ids': list(pair), 'sampling_pool': pool, 'sampling_route': route,
                                         'sampling_score': score, 'split': 'heldout', 'family_ids': sorted(fam),
                                         'family_cap_at_selection': cap})
                        break
                    if taken == quota: break
            if taken == quota: break
        if taken != quota:
            raise ValueError('insufficient_v3_pool:' + pool + ':' + str(taken) + '/' + str(quota))
        details.append({'pool': pool, 'selected': taken, 'routes': dict(counts), 'family_cap': cap,
                        'route_reallocation': 'round_robin_available_routes_fixed_lexical_order'})
    selected.sort(key=lambda p: digest(['display-shuffle-1729', p['pair_id']]))
    for index, pair in enumerate(selected): pair['batch'] = index // 30 + 1
    if len(selected) != 120 or len(used) != 120:
        raise ValueError('incorrect_v3_pair_count')
    return selected, {'selection_details': details, 'max_family_pair_appearances': max(usage.values()), 'seed': 1729}


def audit_selection(pairs, books, prior_packs, cover_hashes, family):
    selected = {i for p in pairs for i in p['book_ids']}
    reviewed = {i for pack in prior_packs for p in pack['pairs'] for i in p['book_ids']}
    old_pairs = {tuple(sorted(p['book_ids'])) for pack in prior_packs for p in pack['pairs']}
    selected_covers = {cover_hashes[i] for i in selected if cover_hashes[i] is not None}
    old_covers = {cover_hashes[i] for i in reviewed if cover_hashes[i] is not None}
    audit = {'previous_pair_overlap': len(old_pairs & {tuple(sorted(p['book_ids'])) for p in pairs}),
             'previous_book_overlap': len(selected & reviewed),
             'previous_known_family_overlap': len({family[i] for i in selected} & {family[i] for i in reviewed}),
             'previous_identical_cover_overlap': len(selected_covers & old_covers),
             'selected_books': len(selected), 'selected_known_families': len({family[i] for i in selected}),
             'selected_cover_unavailable': sum(cover_hashes[i] is None for i in selected)}
    if any(audit[key] for key in ('previous_pair_overlap', 'previous_book_overlap',
                                 'previous_known_family_overlap', 'previous_identical_cover_overlap')):
        raise ValueError('v3_heldout_isolation_failed')
    if not selected <= books.keys(): raise ValueError('selected_book_missing')
    return audit


def prepare(output):
    from features import worker_lock
    # Must fail before touching previous sources or a completed booklet.
    if (output / 'candidates.json').exists():
        raise ValueError('v3_candidate_pack_exists_no_overwrite')
    with worker_lock(output):
        if (output / 'candidates.json').exists():
            raise ValueError('v3_candidate_pack_exists_no_overwrite')
        return _prepare(output)


def _prepare(output):
    from features import rss_bytes
    from review_v3 import render
    started = time.monotonic();cpu_started = time.process_time()
    verify_v2_freeze()
    previous_sha = protected_artifacts()
    preparation, books, _ = feature_material(PREVIOUS)
    prior_packs = [read_json(RUNTIME / run / 'candidates.json') for run in ('run-v1', 'run-v2')]
    cover_hashes = cached_cover_hashes(books)
    # Published v2 byte identities must agree with the original cached inputs.
    for key, value in prior_packs[1]['selected_cover_sha256'].items():
        if cover_hashes[int(key)] != value: raise ValueError('previous_cover_identity_changed')
    eligible, family, isolation = eligible_books(books, prior_packs, cover_hashes)
    routes = neighbors(PREVIOUS, preparation, eligible)
    pools = build_pools(eligible, routes)
    pairs, selection = select(eligible, family, pools)
    isolation.update(selection)
    isolation.update(audit_selection(pairs, books, prior_packs, cover_hashes, family))
    selected_ids = {i for p in pairs for i in p['book_ids']}
    for i in sorted(selected_ids):
        path = cover_file(INPUT, books[i]['cover_path'])
        actual = file_digest(path) if path is not None else None
        if actual != cover_hashes[i]: raise ValueError('selected_cover_changed_after_feature_computation')
    pack = {'schema': SCHEMA, 'sampling_version': VERSION, 'source': preparation['source'],
            'feature_input_id': preparation['pack_id'], 'previous_artifacts_sha256': previous_sha,
            'isolation': isolation, 'pairs': pairs,
            'selected_cover_sha256': {str(i): cover_hashes[i] for i in sorted(selected_ids)},
            'purpose': 'v3_unseen_heldout_annotations_hidden_until_algorithm_freeze'}
    pack['pack_id'] = digest(pack)
    empty = {'schema': LABEL_SCHEMA, 'pack_id': pack['pack_id'], 'annotations': [
        {'pair_id': p['pair_id'], 'label': None, 'reason': '', 'collection_or_partial_overlap': False} for p in pairs]}
    validate_labels(pack, empty)
    images = render(pack, books, INPUT, output / 'review.html', labels_filename='labels.user.v3.json')
    atomic_json(output / 'labels-input.json', empty)
    atomic_json(output / 'books.json', {'source': preparation['source'], 'books': [books[i] for i in sorted(selected_ids)]})
    resources = {'wall_seconds': time.monotonic() - started, 'cpu_seconds': time.process_time() - cpu_started,
                 'peak_rss_bytes': rss_bytes(), 'cpu_threads_requested': 1, 'model_load_count': 0,
                 'model_inference_count': 0, 'nas_verified': False}
    summary = {'pairs': 120, 'batches': 4, 'batch_size': 30,
               'pool_counts': dict(collections.Counter(p['sampling_pool'] for p in pairs)),
               'isolation': isolation, 'resources': resources, **images}
    atomic_json(output / 'preparation-summary.json', summary)
    verify_v2_freeze()
    if previous_sha != protected_artifacts(): raise ValueError('previous_artifact_changed_during_preparation')
    # Publish only when all outputs and immutable-history checks have succeeded.
    atomic_json(output / 'candidates.json', pack)
    print(json.dumps({'status': 'ready_for_user_annotations_v3', **summary}), flush=True)
