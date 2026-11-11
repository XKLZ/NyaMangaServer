"""Offline complete plans, never writes application organization."""
from __future__ import annotations
import collections
from core import checkpoint, digest
from series_titles import position
from series_match import predict, DOWNGRADE

VERSION = 'series-proposal-v1'


def cliques(nodes, edges):
    adjacent = {i: set() for i in nodes}
    for a, b in edges:
        if a in adjacent and b in adjacent:
            adjacent[a].add(b); adjacent[b].add(a)
    out, steps = [], [0]
    def visit(r, p, x):
        checkpoint(); steps[0] += 1
        if steps[0] > 1_000_000 or len(out) > 50000: raise ValueError('series_proposal_budget')
        if not p and not x:
            if len(r) >= 2: out.append(sorted(r))
            return
        pivot = min(p | x, key=lambda u: (-len(p & adjacent[u]), u)) if p or x else None
        for v in sorted(p - adjacent.get(pivot, set())):
            visit(r | {v}, p & adjacent[v], x & adjacent[v])
            p.remove(v); x.add(v)
    visit(set(), set(nodes), set())
    return out


def order_plan(ids, profiles, existing=None):
    positions = {i: position(profiles[i]['views']) for i in ids}
    values = list(positions.values())
    valid = all(v is not None for v in values) and len({v[0] for v in values if v}) == 1 and len(set(values)) == len(values)
    if not valid:
        return {'work_ids': list(existing) if existing else sorted(ids), 'suggested_order': None,
                'state': 'manual_order_required', 'reason': 'missing_mixed_or_duplicate_position'}
    ordered = sorted(ids, key=lambda i: (positions[i][1], i))
    if existing and [i for i in ordered if i in existing] != existing:
        return {'work_ids': list(existing), 'suggested_order': None, 'state': 'manual_order_required', 'reason': 'preserve_existing_order'}
    return {'work_ids': ordered, 'suggested_order': ordered, 'state': 'explicit_position_order', 'reason': None}


def create_plans(corpus, edges, policy):
    buckets = collections.defaultdict(set)
    for a, b in edges:
        for subject in corpus.profiles[a]['subjects'] & corpus.profiles[b]['subjects']:
            buckets[subject].update((a, b))
    found = {}
    for subject in sorted(buckets):
        for ids in cliques(sorted(buckets[subject]), edges):
            key = tuple(ids)
            if key in found: continue
            if len(ids) > 1000: raise ValueError('series_capacity')
            pairs = [corpus.signal(a, b) for n, a in enumerate(ids) for b in ids[n+1:]]
            if any(predict(s, policy) == 'none' for s in pairs): continue
            tier = 'strong' if all(predict(s, policy) == 'strong' for s in pairs) else 'supplementary'
            first = corpus.profiles[ids[0]]
            name = next((v['stem_text'] for v in first['views'] if v['series_main'] == subject), subject)
            found[key] = {'schema': VERSION, 'id': digest(['create_series', ids]), 'kind': 'create_series',
                          'work_ids': ids, 'target_series_id': None, 'suggested_name': name,
                          'tier': tier, 'subject': subject, 'order': order_plan(ids, corpus.profiles),
                          'risks': sorted({r for s in pairs for r in s['risks']}),
                          'input_fingerprints': sorted(s['input_fingerprint'] for s in pairs),
                          'relationship_truth': 'not_fully_labeled_user_review_required'}
            if len(found) > 50000: raise ValueError('series_proposal_budget')
    return sorted(found.values(), key=lambda p: (p['tier'] != 'strong', p['id']))


def join_plan(corpus, query, series, policy, owners=None):
    members = series['work_ids']
    if query in members: return None
    signals = [corpus.signal(query, i) for i in members]
    if not any(predict(s, policy) != 'none' for s in signals): return None
    subjects = set.intersection(*(corpus.profiles[i]['subjects'] for i in members))
    if not subjects & corpus.profiles[query]['subjects']: return None
    risks = {r for s in signals for r in s['risks']}
    if any(not s['eligible'] for s in signals): risks.add('member_relationship_needs_review')
    blocked = bool(owners and query in owners and owners[query] != series['id'])
    if blocked: risks.add('already_in_other_series')
    tier = 'strong' if any(predict(s, policy) == 'strong' for s in signals) and not DOWNGRADE.intersection(risks) and not blocked and 'member_relationship_needs_review' not in risks else 'supplementary'
    return {'schema': VERSION, 'id': digest(['add_to_series', series['id'], query]), 'kind': 'add_to_series',
            'target_series_id': series['id'], 'existing_work_ids': members, 'work_ids': [query],
            'suggested_name': series['name'], 'tier': tier, 'blocked': blocked,
            'order': order_plan(members+[query], corpus.profiles, members), 'risks': sorted(risks),
            'input_fingerprints': [s['input_fingerprint'] for s in signals],
            'relationship_truth': 'virtual_series_demonstration_not_real_group_accuracy'}
