"""Representative title profiles and bounded, revalidated ambiguity choices."""
from __future__ import annotations
import collections
import itertools
from common import digest
from features import check_resources as checkpoint
from series_cluster import assess, CAPACITY
from series_order import order_plan

VERSION = 'series-representative-profile-v2'
TITLE_SUPPORT = .35  # Frozen V1's explicit title-conflict boundary.
AMBIGUITY_GAP = .05  # Frozen V1 grid step, not fitted to these answers.
MAX_ALTERNATIVES = 3


def representative_subject(ids, corpus):
    ids = tuple(sorted(ids))
    totals = {i:0. for i in ids}
    for a,b in itertools.combinations(ids,2):
        value = corpus.signal(a,b)['raw']
        totals[a] += value; totals[b] += value
    representative = min(ids, key=lambda i:(-totals[i],i))
    p = corpus.profiles[representative]
    support, risks = [], set()
    for other in ids:
        if other == representative: continue
        s = corpus.signal(representative,other)
        if s.get('title',0.) < TITLE_SUPPORT or 'different_title_subject' in s['risks']:
            return {'valid':False,'representative':representative,'subject':'',
                    'risks':['representative_title_not_supported'],'view_support':support}
        support.append({'work_id':other,'title_score':s['title'],
                        'edition_ids':s.get('title_support'), 'positions':s.get('positions')})
    # These are actual title views of ONE Work, not aliases inferred along edges.
    views = p.get('views',[])
    view = views[0] if views else None
    subject = view['series_main'] if view else min(p.get('subjects',[]), default='')
    return {'valid':True,'representative':representative,'subject':subject,
            'suggested_name':view['stem_text'] if view else subject,
            'explicit_edition_aliases':[{'edition_id':v['edition_id'],'raw_text':v['raw_text'],
                                       'stem_text':v['stem_text']} for v in views],
            'risks':sorted(risks),'view_support':support,'algorithm':VERSION}


def candidate_groups(query, groups, neighbours):
    return [g for g in groups if query not in g['work_ids'] and
            any(i in neighbours.get(query,set()) for i in g['work_ids'])]


def addition(query, target, corpus, policy, method, subject_check):
    ids = target['work_ids']
    if query in ids or len(ids) >= CAPACITY: return None
    value = assess([*ids,query],corpus,policy,method,subject_check)
    if value is None: return None
    mean = sum(corpus.signal(query,i)[policy['strategy']] for i in ids)/len(ids)
    return {**value,'query_mean':mean}


def boundary_review(groups, seed_edges, corpus, policy, method, subject_check):
    """One simultaneous ambiguity pass, then revalidate remaining groups/options.

    Nothing consults relation labels. Removing boundaries never implies that the
    remaining Works or alternative choices are human-confirmed organizations.
    """
    neighbours = collections.defaultdict(set)
    for a,b in seed_edges: neighbours[a].add(b); neighbours[b].add(a)
    pending, fixed = {}, {i for g in groups if g.get('anchor') for i in g['anchor']['work_ids']}
    for own in groups:
        for query in own['work_ids']:
            checkpoint()
            if query in fixed: continue
            others = [i for i in own['work_ids'] if i != query]
            choices = []
            if others:
                current = addition(query,{'work_ids':others},corpus,policy,method,subject_check)
                if current is not None: choices.append(current)
            for target in candidate_groups(query,groups,neighbours):
                value = addition(query,target,corpus,policy,method,subject_check)
                if value is not None: choices.append(value)
            choices.sort(key=lambda v:(v['tier']!='strong',-v['query_mean']))
            if len(choices) >= 2 and choices[0]['tier']==choices[1]['tier'] and abs(choices[0]['query_mean']-choices[1]['query_mean']) <= AMBIGUITY_GAP+1e-12:
                pending[query] = 'multiple_series_targets_similarly_supported'
    remaining = []
    for group in groups:
        ids = [i for i in group['work_ids'] if i not in pending]
        if not ids: continue
        value = assess(ids,corpus,policy,method,subject_check) if len(ids)>=2 else None
        if len(ids)>=2 and value is None:
            anchor_ids = set(group['anchor']['work_ids']) if group.get('anchor') else set()
            for i in ids:
                if i not in anchor_ids: pending[i]='group_after_boundary_removal_needs_review'
            ids = sorted(anchor_ids)
        if ids: remaining.append({'work_ids':ids,'anchor':group.get('anchor')})
    alternatives = []
    for query, reason in sorted(pending.items()):
        options=[]
        for target in candidate_groups(query,remaining,neighbours):
            value=addition(query,target,corpus,policy,method,subject_check)
            if value is None: continue
            anchor=target.get('anchor')
            target_id=anchor['id'] if anchor else 'cluster:'+digest(target['work_ids'])[:20]
            options.append({'kind':'join_series' if anchor else 'create_series',
                            'target_id':target_id,'target_work_ids':target['work_ids'],
                            'combined_work_ids':sorted([query,*target['work_ids']]),
                            'tier':value['tier'],'support_score':value['query_mean'],
                            'risks':value['risks'],'profile':value['profile'],
                            'order':order_plan([*target['work_ids'],query],corpus.profiles,
                                               anchor['work_ids'] if anchor else None)})
        options.sort(key=lambda p:(p['tier']!='strong',-p['support_score'],p['target_id']))
        alternatives.append({'work_id':query,'reason':reason,'options':options[:MAX_ALTERNATIVES],
                             'qualified_targets':len(options),'options_revalidated_after_removal':True})
    present=[i for g in remaining for i in g['work_ids']]
    before=[i for g in groups for i in g['work_ids']]
    if len(present)!=len(set(present)) or set(present)&set(pending) or set(present)|set(pending)!=set(before):
        raise ValueError('boundary_partition_invariant')
    return {'groups':remaining,'pending':alternatives,'simultaneous_single_pass':True,
            'max_alternatives':MAX_ALTERNATIVES,'ambiguity_gap':AMBIGUITY_GAP}


def proposals(groups, corpus, policy, method, subject_check):
    result=[]
    for group in groups:
        ids=group['work_ids']; anchor=group.get('anchor')
        if len(ids)<2: continue
        added=[i for i in ids if not anchor or i not in anchor['work_ids']]
        if anchor and not added: continue
        value=assess(ids,corpus,policy,method,subject_check)
        if value is None: raise ValueError('invalid_primary_series_group')
        result.append({'schema':'series-proposal-v2','id':'cluster:'+digest(ids)[:20],
                       'kind':'join_series' if anchor else 'create_series',
                       'work_ids':ids,'new_work_ids':added,'target_series_id':anchor['id'] if anchor else None,
                       'existing_work_ids':anchor['work_ids'] if anchor else [],
                       'suggested_name':anchor['name'] if anchor else value['profile'].get('suggested_name',value['profile']['subject']),
                       'tier':value['tier'],'risks':value['risks'],'profile':value['profile'],
                       'support_mean':value['mean'],'support_minimum':value['minimum'],
                       'order':order_plan(ids,corpus.profiles,anchor['work_ids'] if anchor else None),
                       'human_confirmation_required':True,'fully_labeled_group':False})
    return sorted(result,key=lambda p:(p['tier']!='strong',-p['support_mean'],p['id']))
