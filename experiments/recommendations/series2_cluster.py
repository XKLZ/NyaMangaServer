"""Deterministic series partitions; no labels, model inference or application IO."""
from __future__ import annotations
import collections
import heapq
import itertools
import math
from core import checkpoint, digest
from features import check_resources
from series_match import predict

VERSION = 'series-clustering-v2'
FLOOR = .40
CAPACITY = 1000
PAIR_BUDGET = 2_000_000


def legacy_subject(ids, corpus):
    subjects = set.intersection(*(set(corpus.profiles[i]['subjects']) for i in ids))
    return {'valid': bool(subjects), 'representative': min(ids),
            'subject': min(subjects) if subjects else '', 'risks': [], 'view_support': []}


def cross_evidence(left, right, corpus, policy, method):
    """Measure EVERY cross edge, including ones absent from retrieval."""
    if policy is None:
        return None
    scores, risks, strong = [], set(), True
    for a, b in itertools.product(left, right):
        checkpoint()
        s = corpus.signal(a, b)
        score = s[policy['strategy']]
        if not math.isfinite(score):
            raise ValueError('nonfinite_series_score')
        tier = predict(s, policy)
        if method == 'complete' and tier == 'none':
            return None
        if method == 'average' and (score < FLOOR or 'different_title_subject' in s['risks']):
            return None
        if method not in ('complete', 'average'):
            raise ValueError('unsupported_series_linkage')
        scores.append(score)
        risks.update(s['risks'])
        if not s['eligible']:
            risks.add('distinct_work_relationship_needs_review')
        strong = strong and tier == 'strong'
    if not scores or sum(scores)/len(scores) + 1e-12 < policy['supplementary']:
        return None
    return {'mean': sum(scores)/len(scores), 'minimum': min(scores),
            'strong': strong, 'risks': sorted(risks), 'pairs': len(scores)}


def assess(ids, corpus, policy, method, subject_check=legacy_subject):
    ids = tuple(sorted(ids))
    if len(ids) < 2 or len(ids) > CAPACITY or len(set(ids)) != len(ids):
        return None
    # Accumulate all pairs without requiring each individual average-link pair
    # to reach the average threshold. Floor and title conflict remain mandatory.
    scores, risks, strong = [], set(), True
    for a, b in itertools.combinations(ids, 2):
        checkpoint()
        s = corpus.signal(a, b); score = s[policy['strategy']]
        if not math.isfinite(score):
            raise ValueError('nonfinite_series_score')
        tier = predict(s, policy)
        if method == 'complete' and tier == 'none': return None
        if method == 'average' and (score < FLOOR or 'different_title_subject' in s['risks']): return None
        scores.append(score); risks.update(s['risks']); strong = strong and tier == 'strong'
        if not s['eligible']: risks.add('distinct_work_relationship_needs_review')
    if sum(scores)/len(scores) + 1e-12 < policy['supplementary']: return None
    subject = subject_check(ids, corpus)
    if not subject['valid']: return None
    risks.update(subject['risks'])
    return {'mean': sum(scores)/len(scores), 'minimum': min(scores),
            'tier': 'strong' if strong and not subject['risks'] else 'supplementary',
            'risks': sorted(risks), 'profile': subject}


def agglomerate(ids, seed_edges, corpus, policy, method='average',
                subject_check=legacy_subject, anchors=()):
    """Sparse candidate topology, full cross checks, immutable existing anchors.

    A seed only schedules a possible merge. It never proves unobserved edges.
    Accepted merges form a partition; no maximal-clique enumeration is used.
    """
    ids = sorted(set(ids))
    groups, owner, anchored = {}, {}, {}
    for anchor in sorted(anchors, key=lambda s: s['id']):
        members = tuple(anchor['work_ids'])
        if not members or len(members) > CAPACITY or len(set(members)) != len(members):
            raise ValueError('invalid_series_anchor')
        if any(i not in ids or i in owner for i in members): raise ValueError('overlapping_series_anchors')
        key = min(members); groups[key] = tuple(sorted(members)); anchored[key] = anchor
        owner.update({i:key for i in members})
    for i in ids:
        if i not in owner: groups[i] = (i,); owner[i] = i
    neighbours = collections.defaultdict(set)
    for a, b in sorted(set(tuple(sorted(e)) for e in seed_edges)):
        if a not in owner or b not in owner or a == b: raise ValueError('invalid_series_seed')
        x, y = owner[a], owner[b]
        if x != y: neighbours[x].add(y); neighbours[y].add(x)
    heap, attempts, merges, scheduled = [], 0, [], set()

    def schedule(x, y):
        nonlocal attempts
        x, y = sorted((x, y))
        if x == y or x not in groups or y not in groups: return
        key = (groups[x], groups[y])
        if key in scheduled: return
        scheduled.add(key); attempts += 1
        if attempts > PAIR_BUDGET: raise ValueError('series_cluster_budget')
        if x in anchored and y in anchored: return
        if len(groups[x])+len(groups[y]) > CAPACITY: return
        cross = cross_evidence(groups[x], groups[y], corpus, policy, method)
        if cross is None: return
        profile = subject_check(tuple(sorted(groups[x]+groups[y])), corpus)
        if not profile['valid']: return
        priority = cross['minimum'] if method == 'complete' else cross['mean']
        heapq.heappush(heap, (-priority, -cross['minimum'], x, y, groups[x], groups[y]))

    for x in sorted(neighbours):
        for y in sorted(neighbours[x]):
            if x < y: schedule(x, y)
    while heap:
        checkpoint()
        _, _, x, y, gx, gy = heapq.heappop(heap)
        if groups.get(x) != gx or groups.get(y) != gy: continue
        merged = tuple(sorted(gx+gy)); candidates = (neighbours[x] | neighbours[y]) - {x,y}
        anchor = anchored.pop(x, None) or anchored.pop(y, None)
        groups[x] = merged; del groups[y]
        if anchor is not None: anchored[x] = anchor
        for i in gy: owner[i] = x
        neighbours[x] = set(candidates)
        for z in candidates:
            neighbours[z].discard(x); neighbours[z].discard(y); neighbours[z].add(x)
        neighbours.pop(y, None)
        merges.append({'work_ids':list(merged)})
        for z in sorted(candidates): schedule(x,z)
        if len(merges) % 50 == 0: check_resources()
    result = [{'work_ids':list(members), 'anchor':anchored.get(key)} for key,members in sorted(groups.items())]
    flattened = [i for g in result for i in g['work_ids']]
    if sorted(flattened) != ids: raise ValueError('series_partition_invariant')
    return {'groups':result, 'merge_count':len(merges), 'candidate_merges_checked':attempts,
            'method':method, 'version':VERSION, 'input_digest':digest([ids,sorted(seed_edges),policy])}
