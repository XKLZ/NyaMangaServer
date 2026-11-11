"""V4 cover-first pair/Work recommendation and finite grouped-CV selection.

No source/feature writes, model runtime or network. Unknown relationships never
become negatives; callers supply explicit annotated rows separately from signals.
"""
from __future__ import annotations
import collections
import itertools
import math
import analyze_v2 as v2
import title_v3 as titles
from core import checkpoint, digest

VERSION = 'work-cover-recommendation-v4.1'
GRID = tuple(round(.40 + i * .05, 2) for i in range(11))
STRATEGIES = ('sscd', 'sscd_union_fixed_v2')
HARD_RISKS = {'explicit_creator_conflict', 'different_explicit_volume_or_chapter', 'ordinal_type_ambiguity', 'compilation_vs_single_work', 'large_page_count_difference'}
POLICY = {'schema': 'work-recommendation-policy-v4', 'strategies': list(STRATEGIES), 'sscd_grid': list(GRID),
          'strong_precision_target': .95, 'combined_precision_target': .90,
          'strong_minimum_training_predictions': 10, 'combined_minimum_training_predictions': 20,
          'selection': 'feasible_then_combined_recall_strong_recall_fewer_fp_fewer_candidates_conservative_thresholds_strategy_order',
          'no_feasible_strategy': 'no_recommendations_parameters_null_report_failure_not_silent_fallback',
          'all420_are_seen_development': True}


def structures(books):
    aliases = titles.build_aliases(books)
    return {i: titles.structure(b, aliases) for i, b in books.items()}


def pair_signal(a, b, books, parsed, features, v2_parameters):
    s = v2.signals(a, b, parsed, features, books)
    views = titles.pair_views(parsed[a], parsed[b]); risks = []
    if s['creator']['conflict']: risks.append('explicit_creator_conflict')
    if views['explicit_ordinal_conflict']: risks.append('different_explicit_volume_or_chapter')
    if views['ordinal_type_ambiguous']: risks.append('ordinal_type_ambiguity')
    if parsed[a]['compilation_hint'] != parsed[b]['compilation_hint']: risks.append('compilation_vs_single_work')
    if s['pages'] is not None and s['pages'] < .4: risks.append('large_page_count_difference')
    if s['tentative_difference']: risks.append('different_ambiguous_position')
    if views['position_missing_one_side']: risks.append('ordinal_missing_one_side')
    if s['empty_title']: risks.append('missing_title')
    if s['creator']['all_missing']: risks.append('missing_creator_metadata')
    if s['scores']['sscd'] is None: risks.append('missing_cover_feature')
    if s['pages'] is None: risks.append('missing_page_count')
    baseline = v2.predict(s, v2_parameters)
    return {'edition_ids': [a, b], 'sscd': s['scores']['sscd'], 'risks': sorted(set(risks)),
            'fixed_v2_same_work': baseline['action'] == 'same_work', 'fixed_v2_prediction': baseline,
            'title_similarity': s['title'], 'page_ratio': s['pages'],
            'signal_version': VERSION, 'input_fingerprint': digest([books[a], books[b]])}


def decision(signal, parameters):
    if parameters is None:
        return {'tier': 'none', 'sscd': signal['sscd'], 'risks': signal['risks'], 'reasons': ['no_feasible_training_policy'], 'algorithm_version': VERSION}
    cover = signal['sscd']
    v2_route = parameters['strategy'] == 'sscd_union_fixed_v2' and signal['fixed_v2_same_work']
    cover_candidate = cover is not None and cover >= parameters['candidate_threshold']
    cover_strong = cover is not None and cover >= parameters['strong_threshold']
    strong = (cover_strong or v2_route) and not HARD_RISKS.intersection(signal['risks'])
    candidate = cover_candidate or v2_route
    tier = 'strong' if strong else 'supplementary' if candidate else 'none'
    return {'tier': tier, 'sscd': cover, 'risks': signal['risks'], 'reasons': (['sscd_cover_primary'] if cover_candidate else []) + (['fixed_v2_same_work_route'] if v2_route else []),
            'algorithm_version': VERSION, 'score_semantics': 'matching_signal_not_probability'}


def metrics(rows, decisions, strong_only=False):
    primary = [r for r in rows if v2.primary(r)]
    tp = fp = fn = unknown = 0
    for row in primary:
        selected = decisions[row['pair_id']]['tier'] == 'strong' if strong_only else decisions[row['pair_id']]['tier'] in ('strong', 'supplementary')
        positive = row['label'] == 'same_work'
        tp += int(selected and positive); fp += int(selected and not positive); fn += int(not selected and positive)
    for row in rows:
        selected = decisions[row['pair_id']]['tier'] == 'strong' if strong_only else decisions[row['pair_id']]['tier'] in ('strong', 'supplementary')
        if not v2.primary(row) and selected: unknown += 1
    return {'tp': tp, 'fp': fp, 'fn': fn, 'precision': tp / (tp + fp) if tp + fp else None,
            'recall': tp / (tp + fn) if tp + fn else None, 'predictions': tp + fp,
            'precision_wilson95': v2.wilson(tp, tp + fp), 'recall_wilson95': v2.wilson(tp, tp + fn),
            'excluded_selected': unknown, 'primary_rows': len(primary), 'interval_note': 'pair_Wilson_ignores_family_dependence_not_population_guarantee'}


def select_parameters(rows, signals):
    trials = []
    for strategy in STRATEGIES:
        for low in GRID:
            for high in GRID:
                if high < low: continue
                checkpoint()
                params = {'strategy': strategy, 'candidate_threshold': low, 'strong_threshold': high}
                predictions = {r['pair_id']: decision(signals[r['pair_id']], params) for r in rows}
                strong, combined = metrics(rows, predictions, True), metrics(rows, predictions)
                feasible = strong['predictions'] >= 10 and combined['predictions'] >= 20 and strong['precision'] >= .95 and combined['precision'] >= .90
                trials.append({'parameters': params, 'target_met': feasible, 'strong': strong, 'combined': combined})
    feasible = [t for t in trials if t['target_met']]
    def rank(t):
        c, s, p = t['combined'], t['strong'], t['parameters']
        return (c['recall'] or 0, s['recall'] or 0, -c['fp'], -c['predictions'], p['candidate_threshold'], p['strong_threshold'], -STRATEGIES.index(p['strategy']))
    winner = max(feasible, key=rank) if feasible else None
    return {'parameters': winner['parameters'] if winner else None, 'target_met': bool(winner), 'trials': trials,
            'status': 'feasible_training_policy' if winner else 'no_feasible_training_policy', 'selection_policy': POLICY}


def grouped_cross_validation(rows, signals, assignments):
    fold_by_id = {a['pair_id']: a['fold'] for a in assignments}
    if len(assignments) != len(rows) or set(fold_by_id) != {r['pair_id'] for r in rows} or set(fold_by_id.values()) != set(range(5)):
        raise ValueError('invalid_five_fold_assignments')
    component_folds = collections.defaultdict(set)
    for a in assignments: component_folds[a['component_id']].add(a['fold'])
    if any(len(v) != 1 for v in component_folds.values()): raise ValueError('component_leakage')
    node_folds = collections.defaultdict(set)
    for row in rows:
        for node in ['book:' + str(i) for i in row['book_ids']] + ['family:' + f for f in row['family_ids']]:
            node_folds[node].add(fold_by_id[row['pair_id']])
    if any(len(v) != 1 for v in node_folds.values()): raise ValueError('book_or_family_leakage')
    out_of_fold = {}; folds = []
    for fold in range(5):
        training = [r for r in rows if fold_by_id[r['pair_id']] != fold]
        validation = [r for r in rows if fold_by_id[r['pair_id']] == fold]
        selected = select_parameters(training, signals)
        predictions = {r['pair_id']: decision(signals[r['pair_id']], selected['parameters']) for r in validation}
        out_of_fold.update(predictions)
        folds.append({'fold': fold, 'training_pair_ids': [r['pair_id'] for r in training], 'validation_pair_ids': [r['pair_id'] for r in validation],
                      'selection': selected, 'strong': metrics(validation, predictions, True), 'combined': metrics(validation, predictions)})
    return {'schema': 'work-grouped-development-cv-v4', 'folds': folds, 'predictions': out_of_fold,
            'strong': metrics(rows, out_of_fold, True), 'combined': metrics(rows, out_of_fold),
            'scope': 'already_seen_development_grouped_cv_not_independent_holdout'}


def aggregate_work(query, work, signals, parameters):
    members = list(work['edition_ids'])
    if query in members or not members or len(set(members)) != len(members) or work['preferred_edition_id'] not in members:
        raise ValueError('invalid_leave_query_out_work')
    evidence = [{'edition_id': i, 'decision': decision(signals[i], parameters), 'signal': signals[i]} for i in members]
    order = {'strong': 2, 'supplementary': 1, 'none': 0}
    evidence.sort(key=lambda e: (-order[e['decision']['tier']], -(e['signal']['sscd'] if e['signal']['sscd'] is not None else -2), str(e['edition_id'])))
    best = evidence[0]; tier = best['decision']['tier']
    # A contaminated/mixed Work cannot be promoted to strong by one good member.
    conflicting = [e['edition_id'] for e in evidence if HARD_RISKS.intersection(e['signal']['risks'])]
    if tier == 'strong' and conflicting: tier = 'supplementary'
    return {'work_id': work['id'], 'query_edition_id': query, 'tier': tier, 'matching_score': best['signal']['sscd'],
            'supporting_edition_id': best['edition_id'], 'preferred_edition_id': work['preferred_edition_id'],
            'member_evidence': evidence, 'risks': sorted(set(r for e in evidence for r in e['signal']['risks'])),
            'conflicting_member_ids': conflicting, 'algorithm_version': VERSION, 'automatic_merge': False}


def rank_works(candidates, limit=20):
    if limit not in (5, 10, 20): raise ValueError('unsupported_topK')
    if len({c['work_id'] for c in candidates}) != len(candidates): raise ValueError('duplicate_work_candidate')
    order = {'strong': 2, 'supplementary': 1, 'none': 0}
    return sorted((c for c in candidates if c['tier'] != 'none'), key=lambda c: (-order[c['tier']], -(c['matching_score'] if c['matching_score'] is not None else -2), str(c['work_id'])))[:limit]
