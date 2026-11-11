"""Sparse known-label audits, never used by the clustering/ambiguity rules."""
from __future__ import annotations
import collections
import itertools
from series_match import metrics
from analyze_v2 import primary


def audit(proposals, pending, rows, all_work_ids):
    owners, tiers = {}, {}
    for p in proposals:
        for i in p['work_ids']:
            if i in owners: raise ValueError('duplicate_primary_series_membership')
            owners[i]=p['id']; tiers[i]=p['tier']
    choices={r['pair_id']:tiers[a] if a!=b and a in owners and owners.get(b)==owners[a] else 'none'
             for r in rows for a,b in [r['work_ids']]}
    known=collections.defaultdict(set)
    selected=[r for r in rows if primary(r) and not r.get('mapping_conflict')]
    for r in selected:
        a,b=sorted(r['work_ids'])
        if a!=b:known[a,b].add(r['label'])
    unknown=bad=manual=0
    sizes=[]
    for p in proposals:
        conflicts=[]; count=0
        for pair in itertools.combinations(sorted(p['work_ids']),2):
            labels=known.get(pair,set())
            if labels & {'same_work','unrelated'}:conflicts.append(list(pair))
            if not labels:count+=1
        p['known_negative_pairs']=conflicts; p['unknown_pairs']=count
        unknown+=count>0;bad+=bool(conflicts);manual+=p['order']['suggested_order'] is None
        sizes.append(len(p['work_ids']))
    alternatives={tuple(sorted((p['work_id'],i))) for p in pending for o in p['options'] for i in o['target_work_ids']}
    positives=[r for r in selected if r['label']=='same_series']
    fragments=collections.Counter()
    pending_ids={p['work_id'] for p in pending}
    for r in positives:
        a,b=r['work_ids']
        if choices[r['pair_id']]!='none': fragments['same_primary_group']+=1
        elif a in pending_ids or b in pending_ids:fragments['boundary_needs_review']+=1
        elif a in owners and b in owners:fragments['different_primary_groups']+=1
        else:fragments['one_or_both_ungrouped']+=1
    return {'pair_metrics':{k:metrics(rows,choices,k) for k in ('strong','supplementary','combined')},
            'predictions':choices,'primary_groups':len(proposals),'primary_work_count':len(owners),
            'duplicate_memberships':0,'works_outside_primary':len(set(all_work_ids)-set(owners)),
            'pending_works':len(pending),'alternative_count':sum(len(p['options']) for p in pending),
            'max_options_per_work':max((len(p['options']) for p in pending),default=0),
            'known_conflicting_groups':bad,'groups_with_unknown_relations':unknown,
            'manual_order_groups':manual,'group_size_histogram':dict(sorted(collections.Counter(sizes).items())),
            'largest_group':max(sizes,default=0),'groups_at_least_25':sum(n>=25 for n in sizes),
            'groups_at_least_100':sum(n>=100 for n in sizes),'positive_fragmentation':dict(fragments),
            'known_positive_primary_or_option_coverage':sum(choices[r['pair_id']]!='none' or tuple(sorted(r['work_ids'])) in alternatives for r in positives),
            'group_accuracy_claimed':False,'scope':'known_labeled_pair_coverage_not_group_or_library_accuracy'}
