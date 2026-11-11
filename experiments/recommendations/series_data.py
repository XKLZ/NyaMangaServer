"""Read-only series experiment inputs; no database or application imports."""
from __future__ import annotations
import collections
import json
import os
from pathlib import Path

from core import atomic_json, checkpoint, digest, file_digest, reject_symlinks
import analyze_v2 as v2
import v4_data as old

ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / '.runtime/tests/runs/recommendations/series-v1'
VERSION = 'series-data-v1'


def output_path(path):
    path = Path(path).absolute()
    reject_symlinks(path)
    if '..' in path.parts:
        raise ValueError('output_path_traversal')
    path = path.resolve()
    if not path.is_relative_to(OUTPUT) or path == OUTPUT or path.is_relative_to(OUTPUT / 'coordinator'):
        raise ValueError('output_outside_series_or_reserved')
    return path


def private_dir(path):
    path = output_path(path)
    for p in reversed((path, *path.parents)):
        if p.is_relative_to(OUTPUT):
            p.mkdir(mode=0o700, exist_ok=True)
            os.chmod(p, 0o700)
    return path


def immutable(path, value):
    output_path(path)
    if path.exists():
        if old.read_json(path) != json.loads(json.dumps(value)):
            raise ValueError('existing_series_output_changed')
    else:
        atomic_json(path, value)


def experimental_works(books, rows):
    """Only consistent human same_work components provide known membership.

    Components used for CV isolation are NEVER reused as Work membership.
    No implied label is added between members; original pairs remain the metric.
    """
    uf = old.Components()
    for i in books:
        uf.root(i)
    for r in rows:
        if v2.primary(r) and r['label'] == 'same_work':
            uf.join(*r['book_ids'])
    bad = set()
    for r in rows:
        a, b = r['book_ids']
        if v2.primary(r) and r['label'] in ('same_series', 'unrelated') and uf.root(a) == uf.root(b):
            bad.add(uf.root(a))
    members = collections.defaultdict(list)
    for i in sorted(books):
        anchor = i if uf.root(i) in bad else uf.root(i)
        members[anchor].append(i)
    works, mapping = {}, {}
    for ids in members.values():
        wid = 'work:' + str(min(ids))
        works[wid] = {'id': wid, 'edition_ids': ids,
                      'membership_origin': 'human_same_work_consistent_component' if len(ids) > 1 else 'singleton'}
        mapping.update({i: wid for i in ids})
    conflicts = {r['pair_id'] for r in rows if uf.root(r['book_ids'][0]) in bad and uf.root(r['book_ids'][1]) in bad}
    grouped = collections.defaultdict(list)
    for r in rows:
        if v2.primary(r) and r['label'] in ('same_series', 'unrelated'):
            grouped[tuple(sorted(mapping[i] for i in r['book_ids']))].append(r)
    for rr in grouped.values():
        if len({r['label'] for r in rr}) > 1:
            conflicts.update(r['pair_id'] for r in rr)
    return works, mapping, sorted(conflicts)


def historical_diagnostics(rows):
    predictions, hashes = {}, {}
    for name in ('development-report.json', 'heldout-report.json'):
        path = old.SOURCE / name
        hashes[str(path.relative_to(ROOT))] = file_digest(path)
        for r in old.read_json(path)['predictions']:
            predictions[r['pair_id']] = r['prediction']
    misses = []
    for r in rows:
        if not v2.primary(r) or r['label'] != 'same_series':
            continue
        p = predictions[r['pair_id']]
        if p['action'] == 'same_series':
            continue
        risks = p['risks']
        causes = []
        if 'competing_work_and_series_evidence' in risks or p['relation'] == 'same_work':
            causes.append('relationship_competition')
        if any('ordinal' in k for k in risks):
            causes.append('ordinal_ambiguity')
        if any('creator' in k for k in risks):
            causes.append('creator_condition_or_conflict')
        if any(k in risks for k in ('compilation_vs_single_work', 'large_page_count_difference')):
            causes.append('non_series_gate')
        if p['scores']['same_series'] < .82:
            causes.append('below_frozen_series_threshold')
        if not causes:
            causes.append('distinct_entry_or_creator_support_gate')
        misses.append({'pair_id': r['pair_id'], 'causes': causes, 'old_prediction': p,
                       'retrieval': 'historical_any_endpoint_topK_not_directional_recall'})
    return {'source_hashes': hashes, 'misses': misses,
            'cause_counts_overlapping': dict(collections.Counter(c for m in misses for c in m['causes']))}


def load_inputs():
    manifests = [old.verified_manifest(old.SOURCE / part, part == 'evaluation') for part in ('development', 'evaluation')]
    material = old.read_json(old.SOURCE / 'evaluation/books.json')
    if material.get('schema') != 'recommendation-books-v3' or any(set(b) - old.BOOK_FIELDS for b in material['books']):
        raise ValueError('unapproved_fields')
    books = {b['id']: b for b in material['books']}
    if len(books) != 2823 or len(books) != manifests[1]['books']:
        raise ValueError('unexpected_whitelist')
    rows = old.merge_rows([(old.read_json(old.SOURCE / 'development/rows.json'), 'recommendation-development-v3'),
                          (old.read_json(old.SOURCE / 'evaluation/rows.json'), 'recommendation-heldout-v3')], books)
    if len(rows) != 420:
        raise ValueError('expected_420_seen_rows')
    features, metadata, _ = old.load_features(old.SOURCE / 'evaluation', books)
    works, mapping, conflicts = experimental_works(books, rows)
    cover_rows = old.read_json(old.SOURCE / 'evaluation/features-sscd.json')['items']
    fingerprints = {int(i): v['fingerprint'] for i, v in cover_rows.items() if v.get('status') == 'ok'}
    assignments, fold_summary = old.family_folds(rows, cover_fingerprints=fingerprints)
    fold_by_edition = {}
    for r in rows:
        f = next(a['fold'] for a in assignments if a['pair_id'] == r['pair_id'])
        for i in r['book_ids']:
            if i in fold_by_edition and fold_by_edition[i] != f:
                raise ValueError('shared_edition_fold_leak')
            fold_by_edition[i] = f
    for w in works.values():
        if len({fold_by_edition[i] for i in w['edition_ids'] if i in fold_by_edition}) > 1:
            raise ValueError('work_fold_leak')
    hashes = {}
    for part in ('development', 'evaluation'):
        for name in (*old.FILES, 'input-manifest.json'):
            path = old.SOURCE / part / name
            hashes[str(path.relative_to(ROOT))] = file_digest(path)
    return books, rows, features, metadata, works, mapping, conflicts, assignments, fold_summary, hashes


def prepare():
    books, rows, _, _, works, mapping, conflicts, assignments, folds, hashes = load_inputs()
    target = private_dir(OUTPUT / 'data')
    audit = {'schema': VERSION, 'annotation_scope': '420_seen_development_not_independent_holdout',
             'books': len(books), 'rows': len(rows), 'primary': sum(v2.primary(r) and r['pair_id'] not in conflicts for r in rows),
             'labels': dict(collections.Counter(r['label'] for r in rows)), 'works': len(works),
             'multi_edition_works': sum(len(w['edition_ids']) > 1 for w in works.values()),
             'mapping_conflicts': conflicts, 'folds': folds, 'source_hashes': hashes}
    for name, value in [('audit', audit), ('works', {'works': list(works.values()), 'edition_to_work': mapping}),
                        ('rows', {'rows': rows}), ('folds', {'assignments': assignments}),
                        ('historical-diagnostics', historical_diagnostics(rows))]:
        checkpoint()
        immutable(target / (name + '.json'), value)
    return audit
