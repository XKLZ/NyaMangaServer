"""Fixed V4 Work candidate scoring. No inference or experiment imports."""
import baseline as v2
import titles
from common import digest

PARAMETERS = {"strategy": "sscd_union_fixed_v2", "strong_threshold": .75, "candidate_threshold": .65}
V2_PARAMETERS = {"work_threshold": .9, "series_threshold": .72, "title_weight": .86, "image_weight": .04}

VERSION = 'work-cover-recommendation-v4.1'
HARD_RISKS = {'explicit_creator_conflict', 'different_explicit_volume_or_chapter', 'ordinal_type_ambiguity', 'compilation_vs_single_work', 'large_page_count_difference'}


def structures(books):
    aliases = titles.build_aliases(books)
    return {i: titles.structure(b, aliases) for i, b in books.items()}


def pair_signal(a, b, books, parsed, features, v2_parameters, scores_override=None, input_fingerprint=None):
    s = v2.signals(a, b, parsed, features, books, scores_override)
    if scores_override is not None:
        # BLAS is a coarse row computation. Final candidates and threshold-near
        # routes use the original canonical dot, including auxiliary V2 gates.
        provisional = v2.predict(s, v2_parameters)
        values = s['scores']
        near = any(value is not None and any(abs(value-gate) <= 1e-4 for gate in (.65,.75,.85,.90)) for value in values.values())
        near = near or any(abs(provisional['scores'][key]-gate)<=1e-4 for key,gate in (('version',.9),('series',.72)))
        if (values.get('sscd') is not None and values['sscd'] >= .65-1e-4) or provisional['action']=='same_work' or near:
            s['scores'] = v2.features_for_pair(a,b,features)
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
            'signal_version': VERSION, 'input_fingerprint': input_fingerprint or digest([books[a], books[b]])}


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
