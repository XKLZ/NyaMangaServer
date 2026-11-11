"""Read-only V4 data audit. All 420 annotations are already-seen development data.

No model/image/SQLite reader or old experiment runner is invoked. Persisted feature
float32 values are validated and retained, never normalized for a second time.
"""
from __future__ import annotations
import argparse
import collections
import json
import os
from pathlib import Path
import sys

for _key in ('OMP_NUM_THREADS', 'OPENBLAS_NUM_THREADS', 'MKL_NUM_THREADS', 'VECLIB_MAXIMUM_THREADS', 'NUMEXPR_NUM_THREADS'):
    os.environ[_key] = '1'
import numpy as np
import analyze_v2 as v2
from core import atomic_json, checkpoint, digest, file_digest, install_signals, reject_symlinks
from features import FEATURE_VERSIONS, worker_lock

VERSION = 'work-recommendation-data-v4.1'
ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / '.runtime/tests/runs/recommendations/run-v3/analysis'
OUTPUT = ROOT / '.runtime/tests/runs/recommendations/run-v4'
FILES = ('books.json', 'rows.json', 'features-phash.json', 'features-sscd.json', 'features-minilm.json', 'v2-baseline.json')
BOOK_FIELDS = {'id', 'title', 'title_jpn', 'pages', 'tags', 'language_code', 'is_translated', 'is_full_color', 'censorship', 'has_extraneous_ads'}


def read_json(path):
    reject_symlinks(path)
    if not path.is_file() or path.stat().st_size > 64 * 1024**2:
        raise ValueError('invalid_or_oversize_input')
    return json.loads(path.read_text())


def private_directory(path):
    path = Path(path).absolute()
    reject_symlinks(path)
    path = path.resolve()
    root = OUTPUT.resolve()
    if not path.is_relative_to(root) or path.is_relative_to(root / 'coordinator'):
        raise ValueError('output_outside_v4_or_coordinator_owned')
    for parent in reversed((path, *path.parents)):
        if parent.is_relative_to(root):
            parent.mkdir(mode=0o700, exist_ok=True)
            os.chmod(parent, 0o700)
    return path


def immutable_json(path, value):
    if path.exists():
        if read_json(path) != value:
            raise ValueError('existing_v4_output_changed:' + path.name)
    else:
        atomic_json(path, value)


def verified_manifest(directory, evaluation):
    material = read_json(directory / 'input-manifest.json')
    expected = 'recommendation-evaluation-manifest-v3' if evaluation else 'recommendation-development-manifest-v3'
    if material.get('schema') != expected or set(material.get('files', {})) != set(FILES):
        raise ValueError('invalid_source_manifest')
    for name, sha in material['files'].items():
        checkpoint()
        reject_symlinks(directory / name)
        if file_digest(directory / name) != sha:
            raise ValueError('source_digest_changed:' + name)
    return material


def load_features(directory, books):
    values, metadata, status = {}, {}, {}
    for name, version in FEATURE_VERSIONS.items():
        material = read_json(directory / ('features-' + name + '.json'))
        if material.get('algorithm') != name or material.get('version') != version:
            raise ValueError('feature_identity_mismatch')
        values[name] = {}; status[name] = {}
        for raw, row in material['items'].items():
            i = int(raw)
            if i not in books:
                raise ValueError('feature_outside_whitelist')
            status[name][i] = row.get('status')
            if row.get('status') != 'ok':
                continue
            if name == 'phash':
                feature = row['feature']
                if not isinstance(feature, str) or len(feature) != 16 or any(c not in '0123456789abcdef' for c in feature):
                    raise ValueError('bad_phash')
                values[name][i] = int(feature, 16)
            else:
                vector = np.asarray(row['feature'], dtype=np.float32)
                if vector.shape != ((512,) if name == 'sscd' else (384,)) or not np.isfinite(vector).all() or not .999 <= float(np.linalg.norm(vector)) <= 1.001:
                    raise ValueError('bad_embedding')
                values[name][i] = vector  # canonical cache bytes; no re-normalization
        metadata[name] = {k: v for k, v in material.items() if k != 'items'}
    identities = {(m.get('pack_id'), m.get('input_fingerprint')) for m in metadata.values()}
    if len(identities) != 1:
        raise ValueError('feature_context_mismatch')
    return values, metadata, status


class Components:
    def __init__(self):
        self.parent = {}

    def root(self, value):
        self.parent.setdefault(value, value)
        if self.parent[value] != value:
            self.parent[value] = self.root(self.parent[value])
        return self.parent[value]

    def join(self, *values):
        roots = sorted({self.root(v) for v in values})
        for r in roots[1:]:
            self.parent[r] = roots[0]


def merge_rows(materials, books):
    rows = []; seen_pairs = {}; seen_ids = set()
    for material, expected in materials:
        for original in v2.validate_rows(material, books, expected):
            key = tuple(sorted(original['book_ids']))
            if original['pair_id'] in seen_ids or key in seen_pairs:
                raise ValueError('duplicate_or_conflicting_annotation')
            seen_ids.add(original['pair_id']); seen_pairs[key] = original['label']
            rows.append(dict(original))
    return rows


def family_folds(rows, folds=5, cover_fingerprints=None):
    """Both ends of every labeled pair, shared books and known families stay together.

    Components are only isolation units, never inferred Works or inferred truth.
    Balance by row count only; labels and predictions do not choose folds.
    """
    uf = Components()
    for row in rows:
        uf.join(*['book:' + str(i) for i in row['book_ids']], *['family:' + f for f in row['family_ids']])
    for book, fingerprint in (cover_fingerprints or {}).items():
        uf.join('book:' + str(book), 'cover:' + fingerprint)
    groups = collections.defaultdict(list)
    for row in rows:
        groups[uf.root('book:' + str(row['book_ids'][0]))].append(row)
    ordered = sorted(groups.values(), key=lambda rr: (-len(rr), digest(sorted(r['pair_id'] for r in rr))))
    if len(ordered) < folds:
        raise ValueError('insufficient_independent_components')
    bins = [[] for _ in range(folds)]; assignments = []
    for group in ordered:
        fold = min(range(folds), key=lambda f: (len(bins[f]), f))
        bins[fold].extend(group)
        component = digest(sorted(r['pair_id'] for r in group))
        assignments.extend({'pair_id': r['pair_id'], 'fold': fold, 'component_id': component} for r in group)
    return sorted(assignments, key=lambda r: r['pair_id']), {
        'method': 'shared_book_known_family_cover_fingerprint_and_pair_endpoint_components_greedy_size_only',
        'components': len(ordered), 'largest_component_pairs': max(map(len, ordered)),
        'folds': [{'fold': f, 'pairs': len(rr), 'primary': sum(v2.primary(r) for r in rr), 'labels': dict(collections.Counter(r['label'] for r in rr))} for f, rr in enumerate(bins)],
        'independent_unseen_evaluation': False,
        'limitations': ['selected_pairs_not_population_sample', 'unknown_semantic_family_connections_not_exhaustively_reviewed', 'same_family_pairs_correlated'],
    }


def consistency(rows):
    # Explicit same-work connectivity is used only to flag contradictions, not
    # to create positive labels or virtual Work membership through transitivity.
    uf = Components()
    for row in rows:
        if v2.primary(row) and row['label'] == 'same_work':
            uf.join(*['book:' + str(i) for i in row['book_ids']])
    contradictions = []
    for row in rows:
        if v2.primary(row) and row['label'] != 'same_work' and uf.root('book:' + str(row['book_ids'][0])) == uf.root('book:' + str(row['book_ids'][1])):
            contradictions.append({'pair_id': row['pair_id'], 'label': row['label'], 'kind': 'same_work_path_has_explicit_negative_edge'})
    return {'explicit_triangle_issues': v2.label_consistency(rows), 'same_work_path_issues': contradictions,
            'automatic_label_repair': False, 'automatic_transitive_work_expansion': False}


def historical_diagnostics(rows, source):
    predictions = {}; parameters = {}; input_hashes = {}
    for name in ('development-report.json', 'heldout-report.json'):
        report = read_json(source / name)
        input_hashes[name] = file_digest(source / name)
        for row in report['predictions']:
            predictions[row['pair_id']] = row['prediction']; parameters[row['pair_id']] = report['parameters']
    misses = []
    for row in rows:
        if not v2.primary(row) or row['label'] != 'same_work':
            continue
        decision = predictions.get(row['pair_id'])
        if decision is None:
            raise ValueError('historical_prediction_missing')
        if decision['action'] == 'same_work':
            continue
        cover = decision['scores'].get('sscd')
        threshold = parameters[row['pair_id']]['work_threshold']
        risks = decision.get('risks', [])
        causes = []
        if cover is None: causes.append('missing_cover_feature')
        if cover is not None and cover < threshold: causes.append('below_score_threshold')
        if risks: causes.append('risk_gate')
        if 'competing_work_and_series_evidence' in risks or decision['action'] == 'same_series': causes.append('relationship_competition')
        if not causes: causes.append('insufficient_evidence')
        reason = causes[0]
        misses.append({'pair_id': row['pair_id'], 'book_ids': row['book_ids'], 'historical_model': decision['algorithm_version'],
                       'reason': reason, 'causes': causes, 'sscd': cover, 'historical_threshold': threshold, 'risks': risks,
                       'candidate_stage': 'not_measured_per_direction_in_historical_report', 'old_prediction': decision})
    return {'scope': 'read_only_existing_v3_development_and_once_evaluated_reports_not_rerun',
            'input_sha256': input_hashes, 'same_work_positive_pairs': sum(v2.primary(r) and r['label'] == 'same_work' for r in rows),
            'misses': misses, 'primary_reason_counts': dict(collections.Counter(r['reason'] for r in misses)),
            'cause_counts': dict(collections.Counter(c for r in misses for c in r['causes'])),
            'cause_count_semantics': 'overlapping_causes_not_mutually_exclusive_or_summable_miss_count',
            'old_topK_note': 'historical_either_endpoint_coverage_not_reused_as_forward_import_recall'}


def prepare(output=OUTPUT / 'data-reviewed', source=SOURCE):
    output = private_directory(output)
    with worker_lock(output):
        dev = verified_manifest(source / 'development', False)
        evaluation = verified_manifest(source / 'evaluation', True)
        material = read_json(source / 'evaluation/books.json')
        if material.get('schema') != 'recommendation-books-v3' or any(set(b) - BOOK_FIELDS for b in material['books']):
            raise ValueError('invalid_whitelist')
        books = {b['id']: b for b in material['books']}
        if len(books) != len(material['books']) or len(books) != evaluation['books']:
            raise ValueError('invalid_book_count')
        dev_books = read_json(source / 'development/books.json')['books']
        if any(b != books.get(b['id']) for b in dev_books):
            raise ValueError('development_whitelist_record_changed')
        rows = merge_rows([(read_json(source / 'development/rows.json'), 'recommendation-development-v3'), (read_json(source / 'evaluation/rows.json'), 'recommendation-heldout-v3')], books)
        if len(rows) != 420 or dev['pairs'] != 300 or evaluation['pairs'] != 120:
            raise ValueError('expected_420_seen_pairs')
        features, metadata, status = load_features(source / 'evaluation', books)
        cover_rows = read_json(source / 'evaluation/features-sscd.json')['items']
        fingerprints = {int(i): r['fingerprint'] for i, r in cover_rows.items() if r.get('status') == 'ok' and isinstance(r.get('fingerprint'), str)}
        assignment, folds = family_folds(rows, cover_fingerprints=fingerprints)
        audit = {'schema': VERSION, 'rows': len(rows), 'primary_rows': sum(v2.primary(r) for r in rows), 'whitelist_books': len(books),
                 'labeled_books': len({i for r in rows for i in r['book_ids']}),
                 'labels': dict(collections.Counter(r['label'] for r in rows)), 'partial_overlap': sum(r['collection_or_partial_overlap'] for r in rows),
                 'input_sha256': {str((source / part / name).relative_to(ROOT)): file_digest(source / part / name) for part in ('development', 'evaluation') for name in (*FILES, 'input-manifest.json')},
                 'feature_metadata': metadata,
                 'feature_status_counts': {name: dict(collections.Counter(value.values())) for name, value in status.items()},
                 'missing_labeled_features': {name: sorted({i for r in rows for i in r['book_ids']} - set(value)) for name, value in features.items()},
                 'consistency': consistency(rows), 'family_folds': folds,
                 'annotation_scope': 'all420_already_seen_development_and_regression_not_new_holdout',
                 'no_models_loaded': True, 'no_source_database_read': True}
        immutable_json(output / 'rows.json', {'schema': 'recommendation-development-v4', 'rows': rows})
        immutable_json(output / 'folds.json', {'schema': 'recommendation-family-folds-v4', 'assignments': assignment, **folds})
        immutable_json(output / 'audit.json', audit)
        immutable_json(output / 'historical-diagnostics.json', historical_diagnostics(rows, source))
        return audit


def main():
    parser = argparse.ArgumentParser(); parser.add_argument('command', choices=['prepare']); parser.add_argument('--output', type=Path, default=OUTPUT / 'data-reviewed')
    args = parser.parse_args(); install_signals()
    result = prepare(args.output)
    print(json.dumps({k: result[k] for k in ('rows', 'primary_rows', 'whitelist_books', 'labeled_books', 'labels', 'family_folds')}, ensure_ascii=False))


if __name__ == '__main__':
    main()
