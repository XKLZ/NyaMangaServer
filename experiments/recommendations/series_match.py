"""Work-level series signals and predeclared, grouped development selection."""
from __future__ import annotations
import collections
import functools
import itertools
import os
for _name in ('OMP_NUM_THREADS', 'OPENBLAS_NUM_THREADS', 'MKL_NUM_THREADS', 'VECLIB_MAXIMUM_THREADS', 'NUMEXPR_NUM_THREADS'):
    os.environ[_name] = '1'
import numpy as np
import analyze_v2 as v2
from core import checkpoint, digest
from features import check_resources
import series_titles as titles

VERSION = 'series-work-matcher-v1'
STRATEGIES = ('raw', 'dual')
GRID = tuple(round(.4 + .05*i, 2) for i in range(11))
POLICY = {'version': VERSION, 'strategies': STRATEGIES, 'threshold_grid': GRID,
          'strong_precision': .95, 'combined_precision': .85, 'min_strong': 10, 'min_combined': 20,
          'weights': {'title': .76, 'creator': .12, 'semantic': .12}, 'route_topk': 50,
          'no_feasible_policy': 'no_suggestions_not_fallback', 'all_data_seen': True,
          'selection': 'combined recall,strong recall,fewer FP,fewer suggestions,higher thresholds,raw before dual'}


def make_profiles(books, works):
    aliases = v2.build_aliases(books)
    profiles = {}
    for wid, work in sorted(works.items()):
        views = []
        creators = {'artist': set(), 'group': set()}
        for i in work['edition_ids']:
            views.extend({'edition_id': i, **v} for v in titles.title_views(books[i], aliases))
            for ns in creators:
                creators[ns].update(v2.tags(books[i], ns))
        subjects = set()
        for v in views:
            subjects.update(s for s in (v['series_main'], v['tentative_stem'], v['number_prefix']) if len(s) >= 4)
        grams = set().union(*(v2.grams(v['series_stem']) for v in views))
        profiles[wid] = {**work, 'views': views, 'creators': creators, 'subjects': subjects, 'grams': grams}
    return profiles


class Corpus:
    def __init__(self, books, works, features, clean):
        self.books, self.works = books, works
        self.features, self.clean = features['minilm'], clean
        self.profiles = make_profiles(books, works)
        self.ids = sorted(works)
        self.index = {wid: n for n, wid in enumerate(self.ids)}
        self.edition_ids = sorted(self.features)
        membership = {i: wid for wid, w in works.items() for i in w['edition_ids']}
        self.owner_index = np.asarray([self.index[membership[i]] for i in self.edition_ids])
        self.matrix = np.stack([self.features[i] for i in self.edition_ids]) if self.edition_ids else np.zeros((0, 384), dtype=np.float32)
        self.gram_index, self.creator_index = collections.defaultdict(set), collections.defaultdict(set)
        for wid, p in self.profiles.items():
            for g in p['grams']:
                self.gram_index[g].add(wid)
            for ns, values in p['creators'].items():
                for v in values:
                    self.creator_index[ns, v].add(wid)
        self.routes = {}

    def retrieve(self, wid):
        if wid in self.routes:
            return self.routes[wid]
        checkpoint()
        p = self.profiles[wid]
        overlap = collections.Counter(other for g in p['grams'] for other in self.gram_index[g] if other != wid)
        lexical = {other: count/(len(p['grams']) + len(self.profiles[other]['grams']) - count) for other, count in overlap.items()}
        creator_ids = set().union(*(self.creator_index[ns, v] for ns, values in p['creators'].items() for v in values))
        rank = lambda values: sorted(values, key=lambda other: (-lexical.get(other, 0), other))[:50]
        products = np.full(len(self.ids), -2., dtype=np.float32)
        for i in p['edition_ids']:
            if i in self.features:
                current = self.matrix @ self.features[i]
                np.maximum.at(products, self.owner_index, current)
        semantic = [self.ids[n] for n in sorted(range(len(self.ids)), key=lambda n: (-float(products[n]), self.ids[n]))
                    if self.ids[n] != wid and products[n] > -2][:50]
        routes = {'title': rank(lexical), 'creator_title': rank(creator_ids - {wid}), 'semantic': semantic}
        routes['candidates'] = sorted(set(itertools.chain.from_iterable(routes.values())))
        self.routes[wid] = routes
        check_resources()
        return routes

    @functools.lru_cache(maxsize=50000)
    def _signal(self, a, b):
        pa, pb = self.profiles[a], self.profiles[b]
        best = None
        for va, vb in itertools.product(pa['views'], pb['views']):
            c = titles.compare(va, vb)
            key = (c['title'], bool(c['shared_subject']), c['distinct'], -len(c['risks']))
            if best is None or key > best[0]:
                best = (key, c, va, vb)
        if best is None:
            return {'eligible': False, 'raw': 0., 'dual': 0., 'risks': ['missing_title'], 'title': 0.}
        _, title, va, vb = best
        shared = any(pa['creators'][ns] & pb['creators'][ns] for ns in ('artist', 'group'))
        conflict = bool(pa['creators']['artist'] and pb['creators']['artist'] and
                        not pa['creators']['artist'] & pb['creators']['artist'] and
                        not pa['creators']['group'] & pb['creators']['group'])
        raw, clean = -2., -2.
        semantic_support = None
        for i, j in itertools.product(pa['edition_ids'], pb['edition_ids']):
            if i in self.features and j in self.features:
                value = max(-1., min(1., float(np.dot(self.features[i], self.features[j]))))
                if value > raw:
                    raw, semantic_support = value, [i, j]
            if i in self.clean and j in self.clean:
                clean = max(clean, max(-1., min(1., float(np.dot(self.clean[i], self.clean[j])))))
        risks = list(title['risks'])
        if conflict: risks.append('explicit_creator_conflict')
        if not shared: risks.append('creator_support_missing')
        # Missing creators is informational, not an explicit conflict.
        if raw == -2: risks.append('semantic_feature_missing')
        values = {name: round(.76*title['title'] + .12*int(shared) + .12*max(0., raw if name == 'raw' else max(raw, clean)), 8)
                  for name in STRATEGIES}
        return {**values, 'eligible': title['title'] > 0 and title['distinct'],
                'title': title['title'], 'raw_semantic': None if raw == -2 else raw,
                'clean_semantic': None if clean == -2 else clean, 'creator_shared': shared,
                'risks': sorted(set(risks)), 'title_support': [va['edition_id'], vb['edition_id']],
                'semantic_support': semantic_support, 'subject': title['shared_subject'],
                'positions': title['positions'], 'input_fingerprint': digest([a, b, pa['edition_ids'], pb['edition_ids'],
                   [self.books[i] for i in pa['edition_ids']], [self.books[i] for i in pb['edition_ids']]])}

    def signal(self, a, b):
        return self._signal(*sorted((a, b)))


DOWNGRADE = {'explicit_creator_conflict', 'different_title_subject', 'ordinal_type_ambiguity', 'same_position_needs_work_or_part_review'}


def predict(signal, policy, retrieved=True):
    if policy is None or not retrieved or not signal['eligible']:
        return 'none'
    score = signal[policy['strategy']]
    if score < policy['supplementary']:
        return 'none'
    if score >= policy['strong'] and not DOWNGRADE.intersection(signal['risks']):
        return 'strong'
    return 'supplementary'


def metrics(rows, predictions, layer='combined'):
    selected = [r for r in rows if v2.primary(r) and not r.get('mapping_conflict')]
    tp = fp = fn = 0
    for r in selected:
        pred = predictions.get(r['pair_id'], 'none')
        yes = pred == 'strong' if layer == 'strong' else pred == 'supplementary' if layer == 'supplementary' else pred != 'none'
        actual = r['label'] == 'same_series'
        tp += int(yes and actual); fp += int(yes and not actual); fn += int(actual and not yes)
    return {'samples': len(selected), 'tp': tp, 'fp': fp, 'fn': fn, 'suggestions': tp+fp,
            'precision': tp/(tp+fp) if tp+fp else None, 'recall': tp/(tp+fn) if tp+fn else None,
            'precision_wilson95': v2.wilson(tp, tp+fp), 'recall_wilson95': v2.wilson(tp, tp+fn)}


def select_policy(rows, signals, retrieved):
    trials = []
    for strategy in STRATEGIES:
        for strong in GRID:
            for supplementary in GRID:
                checkpoint()
                if supplementary > strong: continue
                p = {'strategy': strategy, 'strong': strong, 'supplementary': supplementary}
                choices = {r['pair_id']: predict(signals[r['pair_id']], p, retrieved[r['pair_id']]) for r in rows}
                hi, both = metrics(rows, choices, 'strong'), metrics(rows, choices)
                feasible = (hi['precision'] is not None and hi['precision'] >= .95 and hi['suggestions'] >= 10 and
                            both['precision'] is not None and both['precision'] >= .85 and both['suggestions'] >= 20)
                rank = ((both['recall'] or 0), (hi['recall'] or 0), -both['fp'], -both['suggestions'], strong,
                        supplementary, -STRATEGIES.index(strategy))
                trials.append({'parameters': p, 'strong': hi, 'combined': both, 'target_met': feasible, 'rank': rank})
    valid = [t for t in trials if t['target_met']]
    winner = max(valid, key=lambda t: t['rank']) if valid else None
    return {'parameters': winner['parameters'] if winner else None, 'feasible': bool(valid),
            'trials': [{k: v for k, v in t.items() if k != 'rank'} for t in trials]}


def grouped_cv(rows, signals, retrieved, assignments):
    folds = {a['pair_id']: a['fold'] for a in assignments}
    editions = collections.defaultdict(set)
    for r in rows:
        for i in r['book_ids']: editions[i].add(folds[r['pair_id']])
    if any(len(f) != 1 for f in editions.values()): raise ValueError('cross_fold_edition')
    predictions, results = {}, []
    for f in range(5):
        train = [r for r in rows if folds[r['pair_id']] != f]
        valid = [r for r in rows if folds[r['pair_id']] == f]
        choice = select_policy(train, signals, retrieved)
        predictions.update({r['pair_id']: predict(signals[r['pair_id']], choice['parameters'], retrieved[r['pair_id']]) for r in valid})
        results.append({'fold': f, 'train_ids': [r['pair_id'] for r in train], 'validation_ids': [r['pair_id'] for r in valid], 'selection': choice})
    return {'folds': results, 'predictions': predictions,
            'metrics': {k: metrics(rows, predictions, k) for k in ('strong', 'supplementary', 'combined')},
            'scope': 'seen_grouped_development_not_new_holdout'}
