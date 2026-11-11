"""Read-only fixed baseline audit, including already-used historical decisions.

Importing old pure scorers is allowed; old load/run/calibrate/evaluate/clean_features
commands are never invoked. Missing clean cache fails this comparison, no inference.
"""
from __future__ import annotations
from pathlib import Path
import numpy as np
import analyze_v2 as v2
import analyze_v3 as v3
import title_v3 as titles
from core import checkpoint, digest, file_digest
from v4_data import read_json, SOURCE, ROOT

CLEAN_VERSION = 'minilm-clean-title-128-mean-l2-v3.1'


def clean_readonly(books, parsed, raw_features, cache_root, model_sha):
    values = {}; hashes = {}; unchanged = hits = 0
    for i in sorted(books):
        checkpoint(); s = parsed[i]
        if not s['clean_text']: continue
        if not s['clean_changed'] and i in raw_features:
            values[i] = raw_features[i]; unchanged += 1; continue
        fingerprint = digest([CLEAN_VERSION, titles.VERSION, model_sha, s['clean_text'][:8192]])
        path = cache_root / (fingerprint + '.json')
        if not path.is_file(): raise ValueError('required_clean_cache_missing_no_inference')
        item = read_json(path)
        if item.get('fingerprint') != fingerprint or item.get('feature_digest') != digest(item.get('feature')) or item.get('model_sha256') != model_sha or item.get('parser_version') != titles.VERSION or item.get('version') != CLEAN_VERSION:
            raise ValueError('required_clean_cache_invalid')
        vector = np.asarray(item['feature'], dtype=np.float32)
        if vector.shape != (384,) or not np.isfinite(vector).all() or not .999 <= float(np.linalg.norm(vector)) <= 1.001:
            raise ValueError('invalid_clean_cached_vector')
        values[i] = vector; hashes[str(path.relative_to(ROOT))] = file_digest(path); hits += 1
    return values, {'cache_hits': hits, 'unchanged_raw': unchanged, 'model_loads': 0, 'cache_sha256': hashes}


def audit(books, rows, features, metadata, source=SOURCE):
    baseline = read_json(source / 'evaluation/v2-baseline.json')
    frozen_v2 = ROOT / '.runtime/tests/runs/recommendations/run-v2/analysis/frozen-policy.json'
    if file_digest(frozen_v2) != baseline['approved_policy_sha256'] or read_json(frozen_v2)['parameters'] != baseline['parameters']:
        raise ValueError('fixed_v2_policy_identity_changed')
    aliases = v2.build_aliases(books); parsed = {i: v2.structure(b, aliases) for i, b in books.items()}
    v2_predictions = {r['pair_id']: v2.predict(v2.signals(*r['book_ids'], parsed, features, books), baseline['parameters']) for r in rows}
    old_v2 = {}
    v2_inputs = {}
    for name in ('calibration-report.json', 'heldout-report.json'):
        path = frozen_v2.parent / name; report = read_json(path); v2_inputs[str(path.relative_to(ROOT))] = file_digest(path)
        for r in report['predictions']: old_v2[r['pair_id']] = r['prediction']
    changed_v2 = [{'pair_id': i, 'historical_action': old['action'], 'canonical_action': v2_predictions[i]['action']} for i, old in old_v2.items() if i in v2_predictions and old['action'] != v2_predictions[i]['action']]
    v3_predictions = {}; v3_changes = []; cache_audits = {}; inputs = {}
    for part, report_name in (('development', 'development-report.json'), ('evaluation', 'heldout-report.json')):
        material = read_json(source / part / 'books.json'); pool = {b['id']: b for b in material['books']}
        rr = read_json(source / part / 'rows.json')['rows']; report = read_json(source / report_name)
        part_aliases = titles.build_aliases(pool)
        pp = {i: titles.structure(b, part_aliases) for i, b in pool.items()}
        clean, cache_audits[part] = clean_readonly(pool, pp, features['minilm'], source / 'feature-cache-v3/minilm', metadata['minilm']['model_sha256'])
        available = {**features, 'clean_minilm': clean}
        old = {r['pair_id']: r['prediction'] for r in report['predictions']}
        for row in rr:
            i = row['pair_id']; s = v3.signal(*row['book_ids'], pool, pp, available); prediction = v3.predict(s, report['parameters']); v3_predictions[i] = prediction
            if any(prediction.get(k) != old[i].get(k) for k in ('action', 'tier', 'relation')):
                v3_changes.append({'pair_id': i, 'historical': {k: old[i].get(k) for k in ('action', 'tier', 'relation')}, 'canonical': {k: prediction.get(k) for k in ('action', 'tier', 'relation')}})
        inputs[report_name] = file_digest(source / report_name)
    new_rows = read_json(source / 'evaluation/rows.json')['rows']
    old_new_metrics = read_json(source / 'heldout-report.json')['fixed_v2']['version']
    new_metrics = v2.metrics(new_rows, {i: p['action'] for i, p in v2_predictions.items()}, 'version')
    new_aggregate_match = all(old_new_metrics[k] == new_metrics[k] for k in ('tp', 'fp', 'fn'))
    return {'schema': 'fixed-baseline-canonical-audit-v4', 'fixed_v2_parameters': baseline['parameters'],
            'fixed_v2_old300_action_changes': changed_v2, 'fixed_v2_compared_historical_pairs': len(old_v2),
            'fixed_v2_new120_historical_aggregate_match': new_aggregate_match,
            'fixed_v2_new120_metrics': new_metrics,
            'fixed_v3_action_tier_relation_changes': v3_changes, 'fixed_v3_compared_historical_pairs': len(v3_predictions),
            'canonical_pipeline_note': 'persisted_float32_not_normalized_again_old_reports_unchanged',
            'historical_input_sha256': {**v2_inputs, **inputs}, 'clean_cache': cache_audits,
            'predictions_fixed_v2': v2_predictions, 'predictions_fixed_v3': v3_predictions,
            'same_frozen_decisions': not changed_v2 and not v3_changes and new_aggregate_match}
