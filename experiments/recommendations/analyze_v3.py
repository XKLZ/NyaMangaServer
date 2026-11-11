"""V3 cover-first pair suggestions, sealed development and one-use heldout evaluation."""
from __future__ import annotations
import argparse
import collections
import itertools
import json
import os
import platform
from pathlib import Path
import resource
import time
for name in ('OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS','VECLIB_MAXIMUM_THREADS','NUMEXPR_NUM_THREADS'):
    os.environ[name] = '1'
import numpy as np
from core import atomic_json, digest, file_digest, reject_symlinks, checkpoint, install_signals
from features import FEATURE_VERSIONS, worker_lock
import analyze_v2 as v2
import title_v3
import semantic_v3

VERSION = 'relationship-analysis-v3.1'
DIRECTORY = Path(__file__).resolve().parents[2] / '.runtime/tests/runs/recommendations/run-v3/analysis'
FILES = ['books.json','rows.json','features-phash.json','features-sscd.json','features-minilm.json','v2-baseline.json']
CODE_FILES = ['analyze_v3.py','title_v3.py','semantic_v3.py','test_analysis_v3.py','analyze_v2.py','core.py','features.py','models.py','model-artifacts.lock.json','requirements-runtime.txt','requirements-lock-macos-arm64.txt']
GRID = {'work_threshold': [.70,.75,.80,.85,.90], 'series_threshold': [.78,.82,.86,.90,.94], 'series_view': ['structural','clean_semantic','dual_semantic']}
POLICY = {'schema':'recommendation-development-policy-v3', 'finite_grid':GRID,
          'selection':'both relations point precision >=.95 with at least10 strong predictions; maximize sum conservative recall, then fewer FP, more TP, higher thresholds, structural before clean before dual; otherwise fixed conservative .85/.90/dual_semantic',
          'primary':v2.PRIMARY_POLICY, 'review_positives_are_FN':True,
          'retrieval':'each route top20 union, stable dedup and final relation score ranking; report actual total top5/10/20',
          'features':'SSCD primary; ordinal/title/creator/page risks; original and cleaned MiniLM unchanged pinned model; labels/notes/overlap/organization/gallery never prediction features',
          'point_precision_target':.95,'minimum_strong_predictions_per_relation':10,'pool':'development496 then released full whitelist; annotated-positive coverage not library recall'}


def read_json(path):
    reject_symlinks(path)
    if path.stat().st_size > 64*1024**2: raise ValueError('analysis_input_too_large')
    return json.loads(path.read_text())


def input_hashes(directory):
    return {name:file_digest(directory/name) for name in FILES+['input-manifest.json']}


def code_hashes():
    return {name:file_digest(Path(__file__).parent/name) for name in CODE_FILES}


def load_inputs(directory, heldout=False):
    manifest = read_json(directory/'input-manifest.json')
    expected = 'recommendation-evaluation-manifest-v3' if heldout else 'recommendation-development-manifest-v3'
    if manifest.get('schema') != expected or set(manifest.get('files',{})) != set(FILES): raise ValueError('invalid_manifest')
    if any(file_digest(directory/name)!=sha for name,sha in manifest['files'].items()): raise ValueError('input_digest_changed')
    material = read_json(directory/'books.json')
    allowed = {'id','title','title_jpn','pages','tags','language_code','is_translated','is_full_color','censorship','has_extraneous_ads'}
    if material.get('schema') != 'recommendation-books-v3' or any(set(b)-allowed for b in material['books']): raise ValueError('unapproved_book_fields')
    books = {b['id']:b for b in material['books']}
    if len(books)!=len(material['books']) or len(books)!=manifest['books']: raise ValueError('invalid_book_count')
    rows_material = read_json(directory/'rows.json')
    rows = v2.validate_rows(rows_material, books, 'recommendation-heldout-v3' if heldout else 'recommendation-development-v3')
    if len(rows)!=manifest['pairs']: raise ValueError('invalid_pair_count')
    features = {}; metadata={}
    for name,version in FEATURE_VERSIONS.items():
        item = read_json(directory/('features-'+name+'.json'))
        if item.get('version') != version or item.get('algorithm') != name or set(map(int,item['items']))-set(books): raise ValueError('feature_identity_mismatch')
        metadata[name]={k:v for k,v in item.items() if k!='items'}
        values={}
        for raw_id,row in item['items'].items():
            if row.get('status')!='ok': continue
            if name=='phash':
                value=row['feature']
                if not isinstance(value,str) or len(value)!=16: raise ValueError('bad_phash')
                values[int(raw_id)]=int(value,16)
            else:
                value=np.asarray(row['feature'],dtype=np.float32)
                if value.shape != ((512,) if name=='sscd' else (384,)) or not np.isfinite(value).all() or not .999<=float(np.linalg.norm(value))<=1.001: raise ValueError('bad_embedding')
                values[int(raw_id)]=value/np.linalg.norm(value)
        features[name]=values
    return books,rows,features,metadata,read_json(directory/'v2-baseline.json')


def signal(a,b,books,structures,features):
    base=v2.signals(a,b,structures,features,books)
    views=title_v3.pair_views(structures[a],structures[b])
    base.update(views)
    base['clean_semantic']=v2.features_for_pair(a,b,{'minilm':features['clean_minilm']})['minilm']
    base['compilation_mismatch']=structures[a]['compilation_hint']!=structures[b]['compilation_hint']
    return base


def scores(s,params):
    cover=max(0.,s['scores'].get('sscd') or 0.)
    raw=max(0.,s['scores'].get('minilm') or 0.)
    clean=max(0.,s['clean_semantic'] or 0.)
    title=max(s['series_title'],s['main_similarity'])
    semantic={'structural':0.,'clean_semantic':clean,'dual_semantic':max(raw,clean)}[params['series_view']]
    series=(.85*title+.15*float(s['creator']['shared'])) if params['series_view']=='structural' else .55*title+.30*semantic+.15*float(s['creator']['shared'])
    # SSCD stays the actual work score; supplementary evidence routes risk, not a title gate.
    return {'same_work':round(cover,8),'same_series':round(series,8),'sscd':s['scores'].get('sscd'),'raw_semantic':raw,'clean_semantic':clean,'title':s['title'],'series_title':title,'page_ratio':s['pages']}


def predict(s,params):
    values=scores(s,params); risks=[]; common=[]
    if s['creator']['conflict']: common.append('explicit_creator_conflict')
    if s['ordinal_type_ambiguous']: common.append('ordinal_type_ambiguity')
    if s['compilation_mismatch']: common.append('compilation_vs_single_work')
    if s['pages'] is not None and s['pages']<.4: common.append('large_page_count_difference')
    if s['empty_title']: risks.append('missing_title')
    if s['creator']['all_missing']: risks.append('missing_creator_metadata')
    if s['scores'].get('sscd') is None: risks.append('missing_cover_feature')
    if s['pages'] is None: risks.append('missing_page_count')
    work_risk=[]
    if s['explicit_ordinal_conflict']: work_risk.append('different_explicit_volume_or_chapter')
    if s['tentative_difference']: work_risk.append('different_ambiguous_position')
    if s['position_missing_one_side']: work_risk.append('ordinal_missing_one_side')
    work=values['same_work']>=params['work_threshold'] and not common and not work_risk
    distinct=s['explicit_ordinal_conflict'] or s['tentative_difference'] or (not s['same_core'] and not s['same_stem'] and not s['same_main'] and s['title']<.93)
    series=values['same_series']>=params['series_threshold'] and s['creator']['shared'] and distinct and not common and not s['empty_title']
    # A near-identical cover but contradictory logical-position evidence needs a human.
    competition=(work and series) or (series and values['same_work']>=params['work_threshold'] and not s['explicit_ordinal_conflict'])
    reasons=[]
    if competition: common.append('competing_work_and_series_evidence')
    if work and not competition:
        relation='same_work'; tier='strong'; reasons=['sscd_cover_primary_compatible_positions']
    elif series and not competition:
        relation='same_series'; tier='strong'; reasons=['related_structural_semantic_title_distinct_entry_shared_creator']
    else:
        tier='review'; relation='same_work' if values['same_work']>=.55 else 'same_series' if values['same_series']>=.65 else 'unrelated'
        reasons=['insufficient_or_conflicting_evidence']; risks+=work_risk
    risks+=common
    return {'action':relation if tier=='strong' else 'defer','relation':relation,'tier':tier,'reasons':reasons,'risks':sorted(set(risks)), 'scores':values,'algorithm_version':VERSION}


def measured(rows,predictions):
    return {task:v2.metrics(rows,{k:v['action'] for k,v in predictions.items()},task) for task in v2.TASKS}


def select_parameters(rows,pairs):
    trials=[]
    for values in itertools.product(*GRID.values()):
        params=dict(zip(GRID,values)); decisions={r['pair_id']:predict(pairs[r['pair_id']],params) for r in rows}; metrics=measured(rows,decisions)
        feasible=all(m['precision'] is not None and m['precision']>=.95 and m['positive_action_predictions']>=10 for m in metrics.values())
        rank=(feasible,sum(m['recall'] or 0 for m in metrics.values()) if feasible else 0,-sum(m['fp'] for m in metrics.values()),sum(m['tp'] for m in metrics.values()),params['work_threshold'],params['series_threshold'],-GRID['series_view'].index(params['series_view']))
        trials.append({'parameters':params,'metrics':metrics,'target_met':feasible,'rank':rank})
    feasible=[t for t in trials if t['target_met']]
    fallback={'work_threshold':.85,'series_threshold':.90,'series_view':'dual_semantic'}
    winner=max(feasible,key=lambda t:t['rank']) if feasible else next(t for t in trials if t['parameters']==fallback)
    return winner['parameters'],[{k:v for k,v in t.items() if k!='rank'} for t in trials],bool(feasible)


def retrieval(rows,books,structures,features,params):
    positives={task:[r for r in rows if v2.primary(r) and r['label']==label] for task,label in v2.TASKS.items()}
    queries=sorted({i for rr in positives.values() for r in rr for i in r['book_ids']}); ranks={}; counts=[]
    for query in queries:
        checkpoint()
        items=[]
        for other in sorted(books):
            checkpoint()
            if query==other: continue
            s=signal(query,other,books,structures,features); value=scores(s,params)
            items.append((other,s,value))
        routes={}
        for name in ('sscd','minilm','phash'):
            routes[name]=[i for i,s,_ in sorted(items,key=lambda x:(-(x[1]['scores'].get(name) if x[1]['scores'].get(name) is not None else -2),x[0])) if s['scores'].get(name) is not None][:20]
        routes['structure']=[i for i,_,_ in sorted(items,key=lambda x:(-x[1]['series_title'],x[0]))[:20]]
        routes['clean_minilm']=[i for i,s,_ in sorted(items,key=lambda x:(-(x[1]['clean_semantic'] if x[1]['clean_semantic'] is not None else -2),x[0])) if s['clean_semantic'] is not None][:20]
        candidate=set(itertools.chain.from_iterable(routes.values())); counts.append(len(candidate))
        ranks[query]={}
        for task,label in v2.TASKS.items():
            ordered=sorted((x for x in items if x[0] in candidate),key=lambda x:(-x[2][label],x[0]))[:20]
            ranks[query][task]={i:n for n,(i,_,_) in enumerate(ordered,1)}
    result={}
    for task,rr in positives.items():
        for k in (5,10,20):
            found=sum(ranks[a][task].get(b,21)<=k or ranks[b][task].get(a,21)<=k for a,b in (r['book_ids'] for r in rr))
            result[f'{task}:final_total_top{k}']={'found':found,'labeled_positive_pairs':len(rr),'coverage':found/len(rr) if rr else None,'maximum_emitted_candidates_per_query':k,'pool_books':len(books),'scope':'annotated_positive_pair_coverage_not_full_library_recall','either_endpoint':True}
    return {'coverage':result,'queries':len(queries),'recall_route_top_k':20,'routes':5,'deduplicated_candidates_min':min(counts,default=0),'deduplicated_candidates_max':max(counts,default=0),'deduplicated_candidates_mean':sum(counts)/len(counts) if counts else 0,'not_each_route_union_topK':True}


def report(rows,pairs,decisions,params,books,structures,features,v2_parameters):
    old_aliases=v2.build_aliases(books)
    old_structures={i:v2.structure(book,old_aliases) for i,book in books.items()}
    old_pairs={r['pair_id']:v2.signals(*r['book_ids'],old_structures,features,books) for r in rows}
    baseline_v2={r['pair_id']:v2.predict(old_pairs[r['pair_id']],v2_parameters)['action'] for r in rows}
    sscd={r['pair_id']:'same_work' if (pairs[r['pair_id']]['scores'].get('sscd') or 0)>=.7 else 'unrelated' for r in rows}
    tiers={}
    primary_count=sum(v2.primary(r) for r in rows)
    for tier in ('strong','review'):
        selected=[r for r in rows if v2.primary(r) and decisions[r['pair_id']]['tier']==tier]
        tiers[tier]={'samples':len(selected),'fraction':len(selected)/primary_count if primary_count else None,'relation_counts':dict(collections.Counter(decisions[r['pair_id']]['relation'] for r in selected)),'correct_relation':sum(decisions[r['pair_id']]['relation']==r['label'] for r in selected),'quality_scope':'review relation is tentative, not adopted recommendation'}
    return {'schema':VERSION,'parameters':params,'samples':len(rows),'primary_samples':sum(v2.primary(r) for r in rows),'primary_policy':v2.PRIMARY_POLICY,'label_counts':dict(collections.Counter(r['label'] for r in rows)),
            'v3':measured(rows,decisions),'fixed_v2':{task:v2.metrics(rows,baseline_v2,task) for task in v2.TASKS},'sscd_single_signal_0.7':{'version':v2.metrics(rows,sscd,'version'),'series':'not_a_series_model'},'tiers':tiers,
            'excluded':{'uncertain':sum(r['label']=='uncertain' for r in rows),'partial_overlap':sum(r['collection_or_partial_overlap'] for r in rows),'rows':[{'pair_id':r['pair_id'],'label':r['label'],'overlap':r['collection_or_partial_overlap'],'prediction':decisions[r['pair_id']]} for r in rows if not v2.primary(r)]},
            'candidate_ranking':retrieval(rows,books,structures,features,params),'predictions':[{**r,'prediction':decisions[r['pair_id']]} for r in rows],
            'risks':dict(collections.Counter(risk for d in decisions.values() for risk in d['risks'])),'score_semantics':'matching_signals_not_probabilities','automatic_transitive_group_expansion':False,
            'limitations':['selected_pairs_not_population_accuracy','unknown_semantic_family_leakage_not_exhaustively_audited','pair_Wilson_intervals_ignore_family_dependence','cover_similarity_not_full_content_identity','no_group_level_consistency_evaluation','Mac_not_N5105_performance','all300_development_including_old90_no_longer_unseen']}


def resource_summary(start,cpu,semantic):
    rss=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss*(1 if platform.system()=='Darwin' else 1024)
    return {'wall_seconds':time.monotonic()-start,'cpu_seconds':time.process_time()-cpu,'peak_rss_bytes':rss,'rss_target_bytes':2*1024**3,'rss_target_met':rss<=2*1024**3,'cpu_threads':1,'nas_verified':False,'host':platform.system()+'/'+platform.machine(),'clean_title_features':semantic}


def verify_gate(directory,frozen):
    freeze_sha=file_digest(directory/'frozen-policy-v3.json')
    if frozen['code_sha256']!=code_hashes() or frozen['input_sha256']!=input_hashes(directory/'development'): raise ValueError('frozen_code_or_development_input_changed')
    if frozen['python']!=platform.python_version() or frozen['numpy']!=np.__version__: raise ValueError('runtime_changed')
    if frozen['policy_sha256']!=file_digest(directory/'development-policy.json') or frozen['development_report_sha256']!=file_digest(directory/'development-report.json'): raise ValueError('frozen_policy_or_development_report_changed')
    approval=read_json(directory/'approval.json'); release=read_json(directory/'heldout.json')
    if approval.get('approved') is not True or approval.get('frozen_policy_sha256')!=freeze_sha or release.get('frozen_policy_sha256')!=freeze_sha: raise ValueError('root_approval_required')
    if release.get('input_manifest_sha256')!=file_digest(directory/'evaluation/input-manifest.json') or release.get('expected_pairs')!=120: raise ValueError('heldout_release_identity_changed')
    for name,sha in frozen['clean_cache_sha256'].items():
        if file_digest(directory/name)!=sha: raise ValueError('frozen_clean_cache_changed')
    return release


def validate_evaluation_identity(metadata, baseline, frozen, directory):
    identity_fields=('model_sha256','version','algorithm','pack_id','input_fingerprint')
    for route in FEATURE_VERSIONS:
        expected=frozen['feature_metadata'][route]
        if any(metadata[route].get(name)!=expected.get(name) for name in identity_fields):
            raise ValueError('heldout_feature_metadata_changed:'+route)
    development_baseline=read_json(directory/'development/v2-baseline.json')
    if any(baseline.get(name)!=development_baseline.get(name) for name in ('parameters','approved_policy_sha256')):
        raise ValueError('heldout_v2_baseline_changed')


def run(mode,directory=DIRECTORY):
    reject_symlinks(directory); directory.mkdir(mode=0o700,parents=True,exist_ok=True)
    with worker_lock(directory):
        freeze_path=directory/'frozen-policy-v3.json'
        if mode=='calibrate' and freeze_path.exists(): raise ValueError('already_frozen_no_recalibration')
        if mode=='evaluate' and (directory/'heldout-evaluation.started').exists(): raise ValueError('heldout_already_started')
        start,cpu=time.monotonic(),time.process_time()
        if mode=='calibrate':
            policy_path=directory/'development-policy.json'
            if policy_path.exists() and read_json(policy_path)!=POLICY: raise ValueError('preregistered_policy_changed')
            atomic_json(policy_path,POLICY)
        else:
            frozen=read_json(freeze_path); verify_gate(directory,frozen)
        source=directory/('development' if mode=='calibrate' else 'evaluation')
        books,rows,features,metadata,baseline=load_inputs(source,mode=='evaluate')
        if mode=='evaluate':
            validate_evaluation_identity(metadata,baseline,frozen,directory)
            if len(rows)!=120: raise ValueError('expected120heldout')
            development_rows=read_json(directory/'development/rows.json')['rows']
            prior_ids={i for row in development_rows for i in row['book_ids']}
            prior_families={f for row in development_rows for f in row['family_ids']}
            if any(prior_ids & set(row['book_ids']) or prior_families & set(row['family_ids']) for row in rows): raise ValueError('heldout_development_isolation_failure')
        aliases=title_v3.build_aliases(books); structures={i:title_v3.structure(b,aliases) for i,b in books.items()}
        clean,semantic,hashes=semantic_v3.clean_features(books,structures,features['minilm'],directory,metadata['minilm']['model_sha256'])
        features['clean_minilm']=clean
        if mode=='evaluate':
            # Preprocessing can fail before this single-use boundary; predictions cannot be rerun.
            verify_gate(directory,frozen)
            fd=os.open(directory/'heldout-evaluation.started',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600); os.close(fd)
        pairs={r['pair_id']:signal(*r['book_ids'],books,structures,features) for r in rows}
        if mode=='calibrate': params,trials,target=select_parameters(rows,pairs)
        else: params=frozen['parameters']
        decisions={r['pair_id']:predict(pairs[r['pair_id']],params) for r in rows}
        result=report(rows,pairs,decisions,params,books,structures,features,baseline['parameters'])
        result['resources']=resource_summary(start,cpu,semantic); result['stage']=mode
        if mode=='calibrate':
            result['finite_trials']=trials; result['joint_target_met']=target
            atomic_json(directory/'development-report.json',result)
            frozen={'schema':'recommendation-frozen-policy-v3','algorithm_version':VERSION,'parameters':params,'policy':POLICY,'policy_sha256':file_digest(directory/'development-policy.json'),'code_sha256':code_hashes(),'input_sha256':input_hashes(source),'clean_cache_sha256':hashes,'feature_metadata':metadata,'python':platform.python_version(),'numpy':np.__version__,'development_report_sha256':file_digest(directory/'development-report.json')}
            atomic_json(freeze_path,frozen)
        else:
            result['frozen_policy_sha256']=file_digest(freeze_path)
            atomic_json(directory/'heldout-report.json',result)
        print(json.dumps({'stage':mode,'samples':len(rows),'v3':result['v3'],'resources':result['resources'],'frozen_sha256':file_digest(freeze_path)}),flush=True)
        return result


if __name__=='__main__':
    install_signals()
    parser=argparse.ArgumentParser(); parser.add_argument('mode',choices=['calibrate','evaluate']); parser.add_argument('--directory',type=Path,default=DIRECTORY)
    args=parser.parse_args()
    try:
        run(args.mode,args.directory)
    except InterruptedError:
        print('{"status":"cancelled; heldout evaluation cannot restart if started marker exists"}',flush=True)
        raise SystemExit(130)
