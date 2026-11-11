"""Offline series CLI. prepare -> calibrate -> report -> verify; immutable outputs."""
from __future__ import annotations
import argparse
import collections
import contextlib
import itertools
import json
import os
from pathlib import Path
import platform
import shutil
import tempfile
import time
from core import atomic_json, checkpoint, digest, file_digest, install_signals, reject_symlinks
from features import check_resources, rss_bytes, worker_lock
import analyze_v2 as v2
import series_data as data
import series_match as match
import series_proposals as plans
import title_v3
import v4_baselines

VERSION = 'series-report-v1'
CODE = ('series_data.py', 'series_titles.py', 'series_match.py', 'series_proposals.py', 'series_run.py',
        'analyze_v2.py', 'analyze_v3.py', 'title_v3.py', 'v4_baselines.py', 'v4_data.py', 'core.py',
        'features.py', 'models.py', 'model-artifacts.lock.json', 'requirements-runtime.txt', 'requirements-lock-macos-arm64.txt',
        'test_series_data.py', 'test_series_match.py')


def code_hashes():
    return {name: file_digest(Path(__file__).parent / name) for name in CODE}


@contextlib.contextmanager
def publication(target):
    target = data.output_path(target)
    data.private_dir(target.parent) if target.parent != data.OUTPUT else None
    if target.exists(): raise ValueError('completed_output_exists_use_verify')
    pending = Path(tempfile.mkdtemp(prefix='.pending-series-', dir=target.parent))
    os.chmod(pending, 0o700)
    try:
        yield pending
        checkpoint()
        if target.exists(): raise ValueError('publication_conflict')
        os.rename(pending, target)
        fd = os.open(target.parent, os.O_RDONLY)
        try: os.fsync(fd)
        finally: os.close(fd)
    finally:
        if pending.exists(): shutil.rmtree(pending)


def inputs():
    values = data.load_inputs()
    books, rows, features, metadata, works, mapping, conflicts, assignments, folds, hashes = values
    aliases = v2.build_aliases(books)
    parsed = {i: title_v3.structure(b, aliases) for i, b in books.items()}
    # Same cleaned semantic view as frozen V3, verified read-only. No model load.
    clean, cache = v4_baselines.clean_readonly(books, parsed, features['minilm'],
                     data.old.SOURCE / 'feature-cache-v3/minilm', metadata['minilm']['model_sha256'])
    hashes.update(cache['cache_sha256'])
    for name in ('model-artifacts.lock.json', 'requirements-runtime.txt', 'requirements-lock-macos-arm64.txt'):
        p = Path(__file__).parent / name
        hashes[str(p.relative_to(data.ROOT))] = file_digest(p)
    corpus = match.Corpus(books, works, features, clean)
    return values, corpus, cache, hashes


def pair_inputs(rows, corpus, mapping, conflicts):
    prepared, signals, retrieved = [], {}, {}
    for r in rows:
        checkpoint()
        a, b = (mapping[i] for i in r['book_ids'])
        prepared.append({**r, 'work_ids': [a, b], 'mapping_conflict': r['pair_id'] in conflicts})
        if a == b:
            signals[r['pair_id']] = {'eligible': False, 'raw': 0., 'dual': 0., 'title': 0., 'risks': ['same_work_excluded']}
            retrieved[r['pair_id']] = False
        else:
            signals[r['pair_id']] = corpus.signal(a, b)
            retrieved[r['pair_id']] = b in corpus.retrieve(a)['candidates']
    return prepared, signals, retrieved


def calibrate():
    data.prepare()
    started, cpu = time.monotonic(), time.process_time()
    with worker_lock(data.OUTPUT):
        target = data.OUTPUT / 'calibration'
        if target.exists(): raise ValueError('calibration_exists_use_verify')
        values, corpus, cache, hashes = inputs()
        books, rows, features, metadata, works, mapping, conflicts, assignments, folds, _ = values
        prepared, signals, retrieved = pair_inputs(rows, corpus, mapping, conflicts)
        cv = match.grouped_cv(prepared, signals, retrieved, assignments)
        fit = match.select_policy(prepared, signals, retrieved)
        fixed = v4_baselines.audit(books, rows, features, metadata)
        if not fixed['same_frozen_decisions']: raise ValueError('baseline_identity_changed')
        hashes.update(fixed['historical_input_sha256'])
        # Legacy audit keys include basenames; normalize them to the source root.
        hashes = {str((data.old.SOURCE / k).relative_to(data.ROOT)) if '/' not in k else k: v for k, v in hashes.items()}
        baseline_metrics = {}
        for label in ('v2', 'v3'):
            choices = {k: 'strong' if p['action'] == 'same_series' else 'none' for k, p in fixed['predictions_fixed_'+label].items()}
            baseline_metrics[label] = match.metrics(prepared, choices)
        resources = {'wall_seconds': time.monotonic()-started, 'cpu_seconds': time.process_time()-cpu,
                     'peak_rss_bytes': rss_bytes(), 'models_loaded': 0, 'inferences': 0, 'cpu_threads': 1,
                     'nas_verified': False, 'clean_cache_hits': cache['cache_hits'], 'raw_clean_unchanged': cache['unchanged_raw']}
        freeze = {'schema': VERSION, 'policy': match.POLICY, 'code_sha256': code_hashes(), 'input_sha256': hashes,
                  'parameters': fit['parameters'], 'scope': 'all420_seen_grouped_development',
                  'work_context': 'membership_from_consistent_human_same_work_not_series_answers', 'folds': folds}
        with publication(target) as out:
            for name, value in [('policy', freeze), ('grouped-cv', cv), ('development-fit', fit),
                                ('pair-inputs', {'rows': prepared, 'signals': signals, 'retrieved': retrieved}),
                                ('baseline-comparison', baseline_metrics), ('baseline-audit', fixed), ('resources', resources)]:
                atomic_json(out / (name+'.json'), value)
            manifest(out)
        return {'oof': cv['metrics'], 'baselines': baseline_metrics, 'fullfit': fit['parameters'], 'resources': resources}


def manifest(directory, extra=None):
    files = {str(p.relative_to(directory)): file_digest(p) for p in sorted(directory.rglob('*')) if p.is_file() and p.name != 'manifest.json'}
    atomic_json(directory/'manifest.json', {'schema': VERSION, 'files': files, **(extra or {})})


def verify(directory):
    directory = data.output_path(directory)
    material = data.old.read_json(directory/'manifest.json')
    for name, sha in material['files'].items():
        checkpoint()
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts: raise ValueError('invalid_manifest_path')
        path = directory/relative; reject_symlinks(path)
        if file_digest(path) != sha: raise ValueError('output_digest_changed')
    actual = {str(p.relative_to(directory)) for p in directory.rglob('*') if p.is_file() and p.name != 'manifest.json'}
    if actual != set(material['files']): raise ValueError('unexpected_output_file')
    return material


def verify_policy():
    verify(data.OUTPUT/'calibration')
    p = data.old.read_json(data.OUTPUT/'calibration/policy.json')
    if p['code_sha256'] != code_hashes() or digest(p['policy']) != digest(match.POLICY):
        raise ValueError('frozen_algorithm_changed')
    for name, sha in p['input_sha256'].items():
        path = data.ROOT/name
        allowed = (data.old.SOURCE, data.ROOT/'.runtime/tests/runs/recommendations/run-v2', Path(__file__).parent)
        if not any(path.resolve().is_relative_to(root.resolve()) for root in allowed): raise ValueError('unapproved_frozen_input')
        reject_symlinks(path)
        if file_digest(path) != sha: raise ValueError('frozen_input_changed')
    return p


def ranking(corpus, rows, mapping, cv):
    folded = {}
    by_pair = {r['pair_id']: r for r in rows}
    for f in cv['folds']:
        for rid in f['validation_ids']:
            q = mapping[by_pair[rid]['book_ids'][0]]
            if q in folded and folded[q] != f['selection']['parameters']: raise ValueError('query_policy_leak')
            folded[q] = f['selection']['parameters']
    queries = []
    ranks = {}
    for q, policy in sorted(folded.items()):
        checkpoint()
        routes = corpus.retrieve(q)
        selected = []
        for other in routes['candidates']:
            s = corpus.signal(q, other); tier = match.predict(s, policy)
            if tier != 'none':
                selected.append({'work_id': other, 'tier': tier, 'score': s[policy['strategy']],
                                 'title_support': s['title_support'], 'risks': s['risks']})
        selected.sort(key=lambda r: (r['tier'] != 'strong', -r['score'], r['work_id']))
        ranks[q] = {p['work_id']: n for n, p in enumerate(selected, 1)}
        queries.append({'query_work_id': q, 'candidate_count': len(routes['candidates']), 'selected_count': len(selected),
                        'final_top20': selected[:20], 'parameters_from_other_four_folds': policy})
    positives = [r for r in rows if v2.primary(r) and not r.get('mapping_conflict') and r['label'] == 'same_series']
    coverage = {str(k): {'found': sum(ranks.get(mapping[r['book_ids'][0]], {}).get(mapping[r['book_ids'][1]], 10**9) <= k for r in positives),
                         'known_positive_pairs': len(positives)} for k in (5, 10, 20)}
    unique = {(mapping[r['book_ids'][0]], mapping[r['book_ids'][1]]) for r in positives}
    unique_coverage = {str(k): {'found': sum(ranks.get(a, {}).get(b, 10**9) <= k for a, b in unique), 'known_query_target_pairs': len(unique)} for k in (5,10,20)}
    return {'queries': queries, 'topk': coverage, 'unique_work_topk': unique_coverage,
            'direction': 'original_left_work_to_right_work_only_all_query_editions_excluded',
            'scope': 'known_label_coverage_not_full_library_recall', 'query_count': len(queries),
            'mean_selected': sum(q['selected_count'] for q in queries)/len(queries) if queries else 0}


def virtual_series(corpus, rows, mapping, policy):
    positives = set()
    for r in rows:
        if v2.primary(r) and not r.get('mapping_conflict') and r['label'] == 'same_series':
            positives.add(tuple(sorted(mapping[i] for i in r['book_ids'])))
    triangles = []
    for ids in plans.cliques(sorted({i for edge in positives for i in edge}), positives):
        if len(ids) < 3: continue
        for trio in itertools.combinations(ids, 3):
            if len(triangles) >= 30: break
            a, b, q = trio
            series = {'id': 'virtual:'+digest([a,b])[:16], 'name': '已确认关系构造的虚拟系列', 'work_ids': [a,b]}
            proposal = plans.join_plan(corpus, q, series, policy)
            triangles.append({'series': series, 'query_work_id': q, 'proposal': proposal,
                              'scope': 'three_direct_positive_work_pairs_not_inferred_truth'})
    return triangles


def report():
    freeze = verify_policy()
    started, cpu = time.monotonic(), time.process_time()
    with worker_lock(data.OUTPUT):
        target = data.OUTPUT/'final'
        if target.exists(): raise ValueError('final_exists_use_verify')
        values, corpus, cache, hashes = inputs()
        books, rows, _, _, works, mapping, conflicts, _, _, _ = values
        prepared = data.old.read_json(data.OUTPUT/'calibration/pair-inputs.json')['rows']
        cv = data.old.read_json(data.OUTPUT/'calibration/grouped-cv.json')
        signals = data.old.read_json(data.OUTPUT/'calibration/pair-inputs.json')['signals']
        retrieved = data.old.read_json(data.OUTPUT/'calibration/pair-inputs.json')['retrieved']
        topk = ranking(corpus, prepared, mapping, cv)
        policy = freeze['parameters']
        edges, seen, counters = set(), set(), collections.Counter()
        for n, wid in enumerate(corpus.ids):
            checkpoint()
            for other in corpus.retrieve(wid)['candidates']:
                edge = tuple(sorted((wid,other)))
                if edge in seen: continue
                seen.add(edge)
                s = corpus.signal(*edge)
                if match.predict(s, policy) != 'none': edges.add(edge)
            if n % 100 == 0:
                print(json.dumps({'phase':'series_proposals','works_done':n,'works':len(works),'qualified_edges':len(edges)}),flush=True)
            if len(edges) > 250000: raise ValueError('series_evidence_budget')
            check_resources()
        proposals = plans.create_plans(corpus, edges, policy)
        virtual = virtual_series(corpus, prepared, mapping, policy)
        diagnostics = []
        for r in prepared:
            rid = r['pair_id']; pred = cv['predictions'][rid]; s = signals[rid]
            flags = []
            if r['label'] == 'same_series' and pred == 'none':
                flags.append('candidate_not_retrieved' if not retrieved[rid] else 'score_or_gate')
            if r['label'] != 'same_series' and pred != 'none': flags.append('false_positive')
            if not v2.primary(r) or r.get('mapping_conflict'): flags.append('excluded_from_primary')
            counters.update(flags)
            diagnostics.append({**r, 'tier':pred, 'signal':s, 'flags':flags})
        # Unlabeled graph edges are unknown, never classified as correct/incorrect.
        known = collections.defaultdict(set)
        for r in prepared:
            if v2.primary(r) and not r.get('mapping_conflict'):
                known[tuple(sorted(mapping[i] for i in r['book_ids']))].add(r['label'])
        group_counts = collections.Counter()
        for p in proposals:
            known_bad = []
            unknown = 0
            for edge in itertools.combinations(p['work_ids'], 2):
                labels = known.get(tuple(sorted(edge)), set())
                if labels & {'unrelated','same_work'}: known_bad.append(list(edge))
                if not labels: unknown += 1
            p['known_conflict_work_pairs'] = known_bad; p['unlabeled_work_pairs'] = unknown
            group_counts['known_conflicting_proposals'] += bool(known_bad)
            group_counts['with_unknown_relationships'] += unknown > 0
            group_counts['manual_order_required'] += p['order']['suggested_order'] is None
        resources = {'wall_seconds':time.monotonic()-started,'cpu_seconds':time.process_time()-cpu,'peak_rss_bytes':rss_bytes(),
                     'cpu_threads':1,'models_loaded':0,'inferences':0,'nas_verified':False}
        result = {'schema':VERSION,'policy_sha256':file_digest(data.OUTPUT/'calibration/policy.json'),
                  'algorithm_version':match.VERSION,'annotation_scope':'420_seen_development_not_independent_holdout',
                  'oof':cv['metrics'],'fullfit_parameters':policy,
                  'baselines':data.old.read_json(data.OUTPUT/'calibration/baseline-comparison.json'),
                  'forward_ranking':topk,'diagnostics':diagnostics,'diagnostic_counts':dict(counters),
                  'proposals':proposals,'virtual_series_scenarios':virtual,'proposal_checks':dict(group_counts),
                  'qualified_edges':len(edges),'resources':resources,
                  'automatic_application_writes':False,'virtual_series_accuracy_claimed':False}
        with publication(target) as out:
            atomic_json(out/'report.json', result)
            atomic_json(out/'proposals.json', {'schema':plans.VERSION,'create_series':proposals,'virtual_join_scenarios':virtual})
            # Rendering is independent of scoring, added with final delivery.
            import series_review
            display = series_review.render(result, corpus, out)
            atomic_json(out/'display-audit.json', display)
            manifest(out, {'algorithm_code_sha256':code_hashes(), 'renderer_sha256':file_digest(Path(__file__).with_name('series_review.py')),
                           'frozen_policy_sha256':result['policy_sha256']})
        return {'oof':result['oof'],'topk':topk['topk'],'proposals':len(proposals),'virtual_series':len(virtual),'resources':resources}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=('prepare','calibrate','report','verify'))
    args = p.parse_args()
    install_signals()
    if args.action == 'prepare':
        x = data.prepare(); result = {k:x[k] for k in ('books','rows','primary','works','multi_edition_works','mapping_conflicts')}
    elif args.action == 'calibrate': result = calibrate()
    elif args.action == 'report': result = report()
    else:
        verify_policy(); final = data.OUTPUT/'final'
        m = verify(final) if final.exists() else verify(data.OUTPUT/'calibration')
        if final.exists() and m['renderer_sha256'] != file_digest(Path(__file__).with_name('series_review.py')): raise ValueError('renderer_changed')
        result = {'verified':True,'files':len(m['files']),'rescored':False}
    print(json.dumps(result,ensure_ascii=False),flush=True)


if __name__ == '__main__': main()
