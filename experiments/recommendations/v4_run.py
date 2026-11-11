"""Reproducible cached-only V4 report, forward ranking and atomic publication.

Run once in a fresh V4 output directory, then `verify` without re-scoring. This is
seen-data development, never a new holdout. No DB/model/network/application access.
"""
from __future__ import annotations
import argparse
import collections
import contextlib
import heapq
import json
import os
from pathlib import Path, PurePosixPath
import platform
import resource
import shutil
import sys
import tempfile
import time

for _name in ('OMP_NUM_THREADS', 'OPENBLAS_NUM_THREADS', 'MKL_NUM_THREADS', 'VECLIB_MAXIMUM_THREADS', 'NUMEXPR_NUM_THREADS'):
    os.environ[_name] = '1'
import numpy as np
import analyze_v2 as v2
import v4_data as data
import v4_match as match
import v4_labels as labels
from core import atomic_json, checkpoint, digest, file_digest, install_signals, reject_symlinks
from features import check_resources, rss_bytes, worker_lock

VERSION = 'work-recommendation-report-v4.1'
CODE_FILES = ('v4_data.py', 'v4_match.py', 'v4_labels.py', 'v4_baselines.py', 'v4_run.py', 'v4_review.py', 'test_v4_data.py', 'test_v4_rules.py', 'test_v4_delivery.py', 'analyze_v2.py', 'analyze_v3.py', 'title_v3.py', 'core.py', 'features.py', 'models.py', 'model-artifacts.lock.json', 'requirements-runtime.txt', 'requirements-lock-macos-arm64.txt')


def checked_output(path):
    path = Path(path).absolute(); reject_symlinks(path); path = path.resolve()
    root = data.OUTPUT.resolve()
    if not path.is_relative_to(root) or path == root or path.is_relative_to(root / 'coordinator'):
        raise ValueError('output_outside_v4_or_reserved')
    return path


def checked_relative(name):
    if not isinstance(name, str): raise ValueError('invalid_manifest_path')
    value = PurePosixPath(name)
    if value.is_absolute() or '..' in value.parts or not value.parts or 'coordinator' in value.parts:
        raise ValueError('unsafe_manifest_path')
    return value


def checked_input(name):
    value = checked_relative(name); path = data.ROOT / str(value); reject_symlinks(path)
    path = path.resolve()
    allowed_runs = data.ROOT / '.runtime/tests/runs/recommendations'
    allowed_covers = data.ROOT / '.runtime/tests/fixtures/treec-recommendations-input/Covers'
    if not path.is_relative_to(allowed_runs) and not path.is_relative_to(allowed_covers):
        raise ValueError('input_outside_recommendation_whitelist')
    return path


@contextlib.contextmanager
def atomic_directory(target):
    target = Path(target).absolute(); reject_symlinks(target)
    target = target.resolve(); root = data.OUTPUT.resolve()
    if not target.is_relative_to(root) or target == root or target.is_relative_to(root / 'coordinator'):
        raise ValueError('output_outside_v4_or_reserved')
    data.private_directory(target.parent)
    if target.exists(): raise ValueError('completed_output_exists_use_verify_or_new_output')
    pending = Path(tempfile.mkdtemp(prefix='.pending-v4-', dir=target.parent)); os.chmod(pending, 0o700)
    try:
        yield pending
        checkpoint()
        if target.exists(): raise ValueError('publication_target_conflict')
        os.rename(pending, target)
        fd = os.open(target.parent, os.O_RDONLY)
        try: os.fsync(fd)
        finally: os.close(fd)
    finally:
        if pending.exists(): shutil.rmtree(pending)


def code_hashes():
    return {name: file_digest(Path(__file__).parent / name) for name in CODE_FILES}


def load_context(policy_dir=None):
    audit = data.prepare()
    books = {b['id']: b for b in data.read_json(data.SOURCE / 'evaluation/books.json')['books']}
    rows = data.read_json(data.OUTPUT / 'data-reviewed/rows.json')['rows']
    folds = data.read_json(data.OUTPUT / 'data-reviewed/folds.json')['assignments']
    features, metadata, _ = data.load_features(data.SOURCE / 'evaluation', books)
    previous = checked_output(policy_dir or data.OUTPUT / 'stage2')
    cv = data.read_json(previous / 'grouped-cv.json'); fitted = data.read_json(previous / 'development-fit.json')
    baseline = data.read_json(previous / 'baseline-audit-reviewed.json')
    if not baseline['same_frozen_decisions'] or not baseline['fixed_v2_new120_historical_aggregate_match']:
        raise ValueError('baseline_audit_not_consistent')
    for name, sha in baseline['historical_input_sha256'].items():
        relative = name if '/' in name else str((data.SOURCE / name).relative_to(data.ROOT))
        path = checked_input(relative)
        if file_digest(path) != sha: raise ValueError('historical_report_changed')
    for material in baseline['clean_cache'].values():
        for name, sha in material['cache_sha256'].items():
            path = checked_input(name)
            if file_digest(path) != sha: raise ValueError('baseline_clean_cache_changed')
    parsed = match.structures(books); parameters = baseline['fixed_v2_parameters']
    signals = {r['pair_id']: match.pair_signal(*r['book_ids'], books, parsed, features, parameters) for r in rows}
    if signals != data.read_json(previous / 'pair-signals.json'):
        raise ValueError('stage2_signals_changed')
    fold_by_id = {a['pair_id']: a['fold'] for a in folds}; choices = {f['fold']: f['selection']['parameters'] for f in cv['folds']}
    for row in rows:
        predicted = match.decision(signals[row['pair_id']], choices[fold_by_id[row['pair_id']]])
        if predicted != cv['predictions'][row['pair_id']]: raise ValueError('stage2_decisions_changed')
    inputs = {str((previous / n).relative_to(data.ROOT)): file_digest(previous / n) for n in ('grouped-cv.json', 'development-fit.json', 'baseline-audit-reviewed.json', 'pair-signals.json')}
    inputs.update({str((data.OUTPUT / 'data-reviewed' / n).relative_to(data.ROOT)): file_digest(data.OUTPUT / 'data-reviewed' / n) for n in ('audit.json', 'rows.json', 'folds.json', 'historical-diagnostics.json')})
    inputs.update(audit['input_sha256'])
    return books, rows, features, parsed, signals, cv, fitted, baseline, folds, inputs


def tier_metrics(rows, predictions):
    supplement = {i: {**p, 'tier': 'supplementary' if p['tier'] == 'supplementary' else 'none'} for i, p in predictions.items()}
    return {'strong': match.metrics(rows, predictions, True), 'combined': match.metrics(rows, predictions), 'supplementary_only': match.metrics(rows, supplement)}


def from_actions(rows, predictions):
    return match.metrics(rows, {i: {'tier': 'strong' if p['action'] == 'same_work' else 'none'} for i, p in predictions.items()}, True)


def trial_rank(trial):
    c, s, p = trial['combined'], trial['strong'], trial['parameters']
    return (c['recall'] or 0, s['recall'] or 0, -c['fp'], -c['predictions'], p['candidate_threshold'], p['strong_threshold'], -match.STRATEGIES.index(p['strategy']))


def strategy_comparison(rows, signals, cv):
    comparisons = {}
    for strategy in match.STRATEGIES:
        predictions = {}; selections = []
        for fold in cv['folds']:
            feasible = [t for t in fold['selection']['trials'] if t['parameters']['strategy'] == strategy and t['target_met']]
            winner = max(feasible, key=trial_rank) if feasible else None
            parameters = winner['parameters'] if winner else None
            selections.append({'fold': fold['fold'], 'parameters': parameters, 'target_met_training': bool(winner)})
            predictions.update({i: match.decision(signals[i], parameters) for i in fold['validation_pair_ids']})
        comparisons[strategy] = {**tier_metrics(rows, predictions), 'folds': selections}
    return comparisons


def ranking_key(candidate):
    order = {'strong': 2, 'supplementary': 1, 'none': 0}
    return (-order[candidate['tier']], -(candidate['matching_score'] if candidate['matching_score'] is not None else -2), str(candidate['work_id']))


def singleton_candidate(query, other, signal, parameters):
    selected = match.decision(signal, parameters)
    return {'work_id': 'virtual:' + str(other), 'query_edition_id': query, 'tier': selected['tier'],
            'matching_score': signal['sscd'], 'supporting_edition_id': other, 'preferred_edition_id': other,
            'member_evidence': [{'edition_id': other, 'decision': selected, 'signal': signal}],
            'risks': signal['risks'], 'conflicting_member_ids': [other] if match.HARD_RISKS.intersection(signal['risks']) else [],
            'algorithm_version': match.VERSION, 'automatic_merge': False}


def forward_ranking(books, rows, parsed, features, baseline_parameters, cv, folds):
    assignments = {f['pair_id']: f['fold'] for f in folds}; parameters = {f['fold']: f['selection']['parameters'] for f in cv['folds']}
    by_query = collections.defaultdict(list); direct_annotations = {}
    for row in rows:
        by_query[row['book_ids'][0]].append(row)
        direct_annotations[tuple(sorted(row['book_ids']))] = row
    queries = []; target_details = {}; coverage = {k: 0 for k in (5, 10, 20)}
    positives = sum(v2.primary(r) and r['label'] == 'same_work' for r in rows)
    for index, query in enumerate(sorted(by_query)):
        checkpoint(); check_resources()
        rr = by_query[query]; fold_set = {assignments[r['pair_id']] for r in rr}
        if len(fold_set) != 1: raise ValueError('query_across_folds')
        fold = next(iter(fold_set)); params = parameters[fold]
        targets = {}
        for row in rr:
            other = row['book_ids'][1]
            signal = match.pair_signal(query, other, books, parsed, features, baseline_parameters)
            candidate = singleton_candidate(query, other, signal, params)
            targets[row['pair_id']] = {'candidate': candidate, 'rank': 1 if candidate['tier'] != 'none' else None, 'label': row['label']}
        # Only these direct annotations have known relation; never infer neighbors.
        known = {other: direct_annotations[tuple(sorted((query, other)))] for other in books if other != query and tuple(sorted((query, other))) in direct_annotations}
        counts = collections.Counter(); selected = []
        # Pool is streamed. Keep at most 20 final candidates, not query x pool signals.
        for other in sorted(books):
            if other == query: continue
            checkpoint()
            signal = match.pair_signal(query, other, books, parsed, features, baseline_parameters)
            candidate = singleton_candidate(query, other, signal, params)
            if candidate['tier'] == 'none': continue
            counts[candidate['tier']] += 1
            if other not in known: counts['unlabeled_selected'] += 1
            elif not v2.primary(known[other]): counts['excluded_annotated_selected'] += 1
            elif known[other]['label'] == 'same_work': counts['known_positive_selected'] += 1
            else: counts['known_negative_selected'] += 1
            key = ranking_key(candidate)
            for info in targets.values():
                if info['rank'] is not None and candidate['work_id'] != info['candidate']['work_id'] and key < ranking_key(info['candidate']): info['rank'] += 1
            selected.append(candidate)
            selected.sort(key=ranking_key)
            if len(selected) > 20: selected.pop()
        for candidate in selected:
            other = candidate['supporting_edition_id']
            candidate['label_suggestions_if_joined'] = labels.suggest_work([books[query], books[other]])
            row = known.get(other)
            candidate['annotated_relation'] = row['label'] if row else 'unknown_unlabeled'
            candidate['relation_scope'] = 'direct_pair_only_not_full_work_ground_truth'
        for row in rr:
            info = targets[row['pair_id']]; target_details[row['pair_id']] = info
            if v2.primary(row) and row['label'] == 'same_work':
                for k in coverage:
                    coverage[k] += int(info['rank'] is not None and info['rank'] <= k)
        count = counts['strong'] + counts['supplementary']
        queries.append({'query_edition_id': query, 'fold': fold, 'parameters': params, 'pool_existing_editions': len(books) - 1,
                        'pool_work_construction': 'single_edition_virtual_works_not_actual_treec_groups',
                        'candidate_counts': {**dict(counts), 'combined': count}, 'final_top20': selected,
                        'direct_pair_ids': [r['pair_id'] for r in rr]})
        if index % 25 == 0: print('forward_queries_completed=' + str(index + 1), flush=True)
    def distribution(name):
        values = [q['candidate_counts'].get(name, 0) for q in queries]
        return {'minimum': min(values, default=0), 'maximum': max(values, default=0), 'mean': sum(values) / len(values) if values else None,
                'p50': float(np.percentile(values, 50)) if values else None, 'p95': float(np.percentile(values, 95)) if values else None,
                'total_query_candidate_occurrences': sum(values)}
    return {'schema': 'forward_edition_virtual_work_retrieval-v4', 'queries': queries, 'target_pairs': target_details,
            'known_positive_forward_topK': {str(k): {'found': v, 'positive_direct_pairs': positives, 'coverage': v / positives if positives else None} for k, v in coverage.items()},
            'workload': {name: distribution(name) for name in ('strong', 'supplementary', 'combined', 'unlabeled_selected', 'known_negative_selected')},
            'orientation': 'original_book_ids_0_query_to_1_target_not_either_endpoint', 'query_removed_from_pool': True,
            'fold_policy': 'query_own_fold_four_fold_training_no_fullfit_ranking',
            'quality_scope': 'known_direct_pair_forward_coverage_not_full_library_or_real_group_precision',
            'unknown_relations_counted_as_errors': False, 'anonymous_multi_member_tests_separate': True}


def diagnostics(rows, signals, cv, forward):
    out = []
    for row in rows:
        i = row['pair_id']; decision = cv['predictions'][i]; signal = signals[i]; target = forward['target_pairs'][i]
        positive = row['label'] == 'same_work'; eligible = decision['tier'] != 'none'
        flags = []
        if not v2.primary(row): flags.append('excluded_from_primary')
        else:
            if positive and decision['tier'] != 'strong': flags.append('strong_false_negative')
            if positive and not eligible: flags.append('combined_false_negative')
            if not positive and eligible: flags.append('combined_false_positive')
            if not positive and decision['tier'] == 'strong': flags.append('strong_false_positive')
            if positive and target['rank'] is not None and target['rank'] > 20: flags.append('positive_truncated_after_top20')
        if decision['tier'] == 'supplementary': flags.append('supplementary_candidate')
        causes = []
        if signal['sscd'] is None: causes.append('missing_cover_feature')
        if not eligible: causes.append('below_cover_threshold_and_no_fixed_v2_work_route')
        if decision['tier'] == 'supplementary' and match.HARD_RISKS.intersection(signal['risks']): causes.append('explicit_risk_demotes_strong_not_removed')
        if decision['tier'] == 'supplementary' and not match.HARD_RISKS.intersection(signal['risks']): causes.append('below_strong_threshold_but_supplementary_retained')
        if target['rank'] is not None and target['rank'] > 20: causes.append('final_top20_ranking_truncation')
        out.append({**row, 'flags': flags, 'causes': causes, 'prediction': decision, 'signal': signal, 'forward_rank': target['rank'],
                    'label_suggestions_if_joined': None})
    return {'rows': out, 'flags': dict(collections.Counter(f for r in out for f in r['flags'])),
            'miss_cause_counts': dict(collections.Counter(c for r in out if 'combined_false_negative' in r['flags'] for c in r['causes'])),
            'cause_count_semantics': 'overlapping_reasons', 'review_is_not_user_label_error': True}


def anonymous_examples():
    def b(i, language, **properties):
        return {'id': i, 'title': '匿名版本' + str(i), 'tags': [], 'languages': [language] if language else [],
                'manual_properties': {d: {'value': v} for d, v in properties.items()}}
    examples = [
        ('人工值和自定义标签保护', [{**b(1, 'chinese', color='color'), 'custom_labels': ['彩色', '个人修订']}, b(2, 'chinese', color='monochrome')]),
        ('语言旁示已足够，无须额外标签', [b(1, 'chinese', color='color'), b(2, 'japanese', color='color')]),
        ('特别须有明确普通版对照', [b(1, 'chinese', special='special'), b(2, 'chinese', special='standard')]),
        ('未知不当反面证据', [b(1, 'chinese', color='color'), b(2, 'chinese')]),
    ]
    return [{'name': name, 'scope': 'anonymous_rule_examples_not_user_editions_or_quality_measurements', 'suggestions': labels.suggest_work(editions)} for name, editions in examples]


def explicit_triangles(books, rows, parsed, features, baseline_parameters, cv, folds):
    edges = {tuple(sorted(r['book_ids'])): r for r in rows if v2.primary(r) and r['label'] == 'same_work'}
    neighbors = collections.defaultdict(set)
    for a, b in edges: neighbors[a].add(b); neighbors[b].add(a)
    assignments = {f['pair_id']: f['fold'] for f in folds}
    parameters = {f['fold']: f['selection']['parameters'] for f in cv['folds']}
    examples = []
    for a in sorted(neighbors):
        for b in sorted(i for i in neighbors[a] if i > a):
            for c in sorted(i for i in neighbors[a] & neighbors[b] if i > b):
                pair_rows = [edges[(a, b)], edges[(a, c)], edges[(b, c)]]
                fold_set = {assignments[r['pair_id']] for r in pair_rows}
                if len(fold_set) != 1: raise ValueError('explicit_triangle_fold_leakage')
                work = {'id': 'explicit-triangle:' + digest([a, b, c])[:16], 'edition_ids': [b, c], 'preferred_edition_id': b}
                signals = {i: match.pair_signal(a, i, books, parsed, features, baseline_parameters) for i in (b, c)}
                result = match.aggregate_work(a, work, signals, parameters[next(iter(fold_set))])
                examples.append({'query_edition_id': a, 'existing_work': work, 'explicit_pair_ids': [r['pair_id'] for r in pair_rows], 'result': result,
                                 'labels_if_joined': labels.suggest_work([books[i] for i in (a, b, c)]),
                                 'scope': 'complete_explicit_human_triangle_demonstration_only_not_accuracy_sample_no_transitive_guessing'})
    return examples


def prepare_policy(directory):
    """Explicit seen-development re-fit in a new directory, never overwrites stage2."""
    from v4_baselines import audit as audit_baselines
    data.private_directory(data.OUTPUT)
    with worker_lock(data.OUTPUT), atomic_directory(directory) as pending:
        data.prepare()
        books = {b['id']: b for b in data.read_json(data.SOURCE / 'evaluation/books.json')['books']}
        rows = data.read_json(data.OUTPUT / 'data-reviewed/rows.json')['rows']
        assignments = data.read_json(data.OUTPUT / 'data-reviewed/folds.json')['assignments']
        features, metadata, _ = data.load_features(data.SOURCE / 'evaluation', books)
        baseline = audit_baselines(books, rows, features, metadata)
        if not baseline['same_frozen_decisions']: raise ValueError('baseline_changed_during_reproduction')
        parsed = match.structures(books)
        signals = {r['pair_id']: match.pair_signal(*r['book_ids'], books, parsed, features, baseline['fixed_v2_parameters']) for r in rows}
        atomic_json(pending / 'pair-signals.json', signals)
        atomic_json(pending / 'grouped-cv.json', match.grouped_cross_validation(rows, signals, assignments))
        atomic_json(pending / 'development-fit.json', match.select_parameters(rows, signals))
        atomic_json(pending / 'baseline-audit-reviewed.json', baseline)
    print(json.dumps({'prepared_policy': str(directory), 'scope': 'seen_development_explicit_refit_not_holdout'}))


def run(output, policy_dir=None):
    started = time.monotonic(); cpu = time.process_time(); initial_code = code_hashes()
    data.private_directory(data.OUTPUT)
    with worker_lock(data.OUTPUT), atomic_directory(output) as pending:
        books, rows, features, parsed, signals, cv, fitted, baseline, folds, inputs = load_context(policy_dir)
        check_resources()
        forward_started = time.monotonic()
        forward = forward_ranking(books, rows, parsed, features, baseline['fixed_v2_parameters'], cv, folds)
        forward_seconds = time.monotonic() - forward_started
        detail = diagnostics(rows, signals, cv, forward)
        for row in detail['rows']:
            a, b = row['book_ids']; row['label_suggestions_if_joined'] = labels.suggest_work([books[a], books[b]])
        baseline_metrics = {
            'fixed_v2': from_actions(rows, baseline['predictions_fixed_v2']),
            'fixed_v3': from_actions(rows, baseline['predictions_fixed_v3']),
            'sscd_only_0_70_no_risk_gate': match.metrics(rows, {i: {'tier': 'strong' if s['sscd'] is not None and s['sscd'] >= .7 else 'none'} for i, s in signals.items()}, True),
        }
        comparison = strategy_comparison(rows, signals, cv)
        report = {'schema': VERSION, 'algorithm_version': match.VERSION, 'preset_rule_version': labels.VERSION,
                  'annotation_scope': 'all420_seen_development_grouped_cv_not_new_holdout',
                  'samples': len(rows), 'primary_rows': sum(v2.primary(r) for r in rows),
                  'oof_pair_metrics': tier_metrics(rows, cv['predictions']), 'fixed_baselines': baseline_metrics,
                  'strategy_grouped_cv': comparison, 'development_fit': {'parameters': fitted['parameters'], 'target_met_training': fitted['target_met'], 'not_used_for_oof_ranking': True},
                  'fold_parameters': [{'fold': f['fold'], 'parameters': f['selection']['parameters'], 'target_met_training': f['selection']['target_met']} for f in cv['folds']],
                  'forward_retrieval': forward, 'diagnostics': detail, 'anonymous_label_examples': anonymous_examples(),
                  'explicit_multi_member_examples': explicit_triangles(books, rows, parsed, features, baseline['fixed_v2_parameters'], cv, folds),
                  'display_data_status': {'custom_labels': 'not_in_whitelist_export_unverified_not_empty_truth', 'manual_properties': 'not_in_whitelist_export_unverified_not_empty_truth'},
                  'limitations': ['seen_selected_pair_development_not_population_accuracy', 'pair_Wilson_intervals_ignore_family_correlation', 'unknown_semantic_families_not_exhaustively_reviewed', 'unknown_full_pool_relations_not_scored_as_FP_or_TP', 'singleton_virtual_work_coverage_not_real_multi_member_group_precision', 'no_real_manual_property_or_custom_label_export', 'Mac_resources_not_N5105_verified', 'cover_similarity_not_complete_content_equivalence', 'no_application_integration_or_automatic_merge']}
        atomic_json(pending / 'report.json', report)
        atomic_json(pending / 'threshold-comparison.json', {'schema': 'work-threshold-comparison-v4', 'full_development_trials': fitted['trials'], 'scope': 'all_seen_training_sweep_not_out_of_fold_quality'})
        # Renderer copies only validated covers from the explicit source copy.
        from v4_review import render
        cover_summary = render(report, books, pending)
        inputs.update(cover_summary.pop('private_input_sha256'))
        resources = {'schema': 'recommendation-resources-v4', 'wall_seconds': time.monotonic() - started, 'cpu_seconds': time.process_time() - cpu,
                     'forward_ranking_seconds': forward_seconds, 'peak_rss_bytes': rss_bytes(), 'rss_target_bytes': 2 * 1024**3,
                     'rss_target_met': rss_bytes() <= 2 * 1024**3, 'cpu_threads': 1, 'model_loads': 0, 'model_inferences': 0,
                     'host': platform.system() + '/' + platform.machine(), 'nas_verified': False, 'cover_copy': cover_summary}
        atomic_json(pending / 'resources.json', resources); check_resources()
        if initial_code != code_hashes(): raise ValueError('source_changed_during_report_run')
        manifest = {'schema': 'work-recommendation-artifacts-v4', 'code_sha256': initial_code, 'input_sha256': inputs,
                    'files': {str(p.relative_to(pending)): file_digest(p) for p in sorted(pending.rglob('*')) if p.is_file()},
                    'parameters': report['fold_parameters'], 'fullfit_parameters': fitted['parameters'],
                    'python': platform.python_version(), 'numpy': np.__version__, 'models_loaded': False,
                    'input_context_fingerprint': digest(inputs), 'automatic_writes_to_application': False}
        atomic_json(pending / 'manifest.json', manifest)
        checkpoint()
    print(json.dumps({'completed': str(output), 'strong': report['oof_pair_metrics']['strong'], 'combined': report['oof_pair_metrics']['combined'],
                      'supplementary': report['oof_pair_metrics']['supplementary_only'], 'forward': forward['known_positive_forward_topK'], 'resources': resources}, ensure_ascii=False))


def verify(output):
    output = checked_output(output); manifest = data.read_json(output / 'manifest.json')
    if manifest['code_sha256'] != code_hashes(): raise ValueError('report_code_changed')
    for name, sha in manifest['input_sha256'].items():
        path = checked_input(name)
        if file_digest(path) != sha: raise ValueError('report_input_changed')
    for name, sha in manifest['files'].items():
        path = output / str(checked_relative(name)); reject_symlinks(path)
        if not path.resolve().is_relative_to(output.resolve()) or file_digest(path) != sha: raise ValueError('report_artifact_changed')
    print(json.dumps({'verified': True, 'files': len(manifest['files']), 'models_loaded': False, 'rescored': False}))


def main():
    parser = argparse.ArgumentParser(); parser.add_argument('command', choices=['prepare-policy', 'run', 'verify']); parser.add_argument('--output', type=Path, default=data.OUTPUT / 'final')
    parser.add_argument('--policy-dir', type=Path, default=data.OUTPUT / 'stage2')
    args = parser.parse_args(); install_signals()
    if args.command == 'prepare-policy': prepare_policy(args.policy_dir)
    elif args.command == 'run': run(args.output, args.policy_dir)
    else: verify(args.output)


if __name__ == '__main__': main()
