"""Frozen Series V2 strategy on actual Works; no labels or source archive IO."""
import base64
import itertools
import numpy as np
from common import digest
import collections
import json
from features import vector, check_resources
from series_matching import Corpus, predict
from series_cluster import agglomerate, assess
from series_profiles import representative_subject, boundary_review, proposals

VERSION = 'series-recommendation-v2.1'
PARAMETERS = {'strategy':'raw','strong':.70,'supplementary':.45}
MAX_EDGES = 250_000


class SeriesDiscovery:
    def __init__(self, request, validate_book, previous=None):
        items=request['books'];works=request['works'];self.key=request['pool_key']
        if not isinstance(items,list) or not 1<=len(items)<=50000 or not isinstance(works,list) or not 1<=len(works)<=6000:
            raise ValueError('series_pool_limit')
        for b in items:validate_book(b)
        books={b['id']:{k:v for k,v in b.items() if k!='custom_labels'} for b in items}
        if len(books)!=len(items) or len({b.get('library_id') for b in items})!=1:raise ValueError('invalid_series_library')
        mapped={};owners=set()
        for w in works:
            ids=w['edition_ids'];wid=w['id']
            if wid in mapped or not isinstance(ids,list) or not 1<=len(ids)<=100 or len(set(ids))!=len(ids) or any(i not in books or i in owners for i in ids):
                raise ValueError('invalid_series_work')
            mapped[wid]={'id':wid,'edition_ids':sorted(ids)};owners.update(ids)
        if owners!=set(books):raise ValueError('incomplete_series_work_partition')
        vectors={}
        for i,value in request.get('features',{}).items():
            if i not in books:raise ValueError('invalid_series_feature_owner')
            if isinstance(value,dict):
                if set(value)!={'encoding','data'} or value['encoding']!='f32le':raise ValueError('invalid_series_vector_encoding')
                value=np.frombuffer(base64.b64decode(value['data'],validate=True),dtype='<f4')
            vectors[i]=vector(value,'minilm')
        self.identity=digest([items,works,request.get('features',{})])
        if isinstance(previous,SeriesDiscovery) and previous.identity==self.identity:
            self.corpus=previous.corpus
        else:
            # The bounded method cache contains self references. Clear it before
            # replacing the pool, so repeated resolves cannot retain old pools.
            Corpus._signal.cache_clear()
            self.corpus=Corpus(books,mapped,{'minilm':vectors},vectors)
        def canonical(value):
            if isinstance(value,dict):return {k:canonical(v) for k,v in value.items()}
            if isinstance(value,set):return sorted(canonical(v) for v in value)
            if isinstance(value,(list,tuple)):return [canonical(v) for v in value]
            return value
        self.profiles={wid:digest(['series-signals-v1',canonical(p),
            [(i,{k:v for k,v in books[i].items() if k!='custom_labels'},request.get('features',{}).get(i)) for i in p['edition_ids']]])
            for wid,p in self.corpus.profiles.items()}
        self.corpus_identity=digest(self.profiles)
        self.corpus.persistent_signals={};self.corpus.signal_delta=collections.OrderedDict()
        self.corpus.computed_signal_keys=set();self.corpus.reused_signal_keys=set()
        self.corpus.pair_key=lambda a,b:digest(['series-signals-v1',a,b,self.profiles[a],self.profiles[b]])
        self.route_delta={}
        self.anchors=[];self.unavailable=[];self.blocked=set();anchor_owners=set()
        for s in request.get('series',[]):
            members=s['work_ids']
            if not members:continue
            if len(set(members))!=len(members) or len(members)>1000 or any(i in anchor_owners for i in members):raise ValueError('invalid_series_anchor')
            anchor_owners.update(members)
            if all(i in mapped for i in members):self.anchors.append(s)
            else:self.unavailable.append(s['id']);self.blocked.update(i for i in members if i in mapped)
        self.ids=[i for i in self.corpus.ids if i not in self.blocked]
        check_resources()

    def row(self,request):
        if request['pool_key']!=self.key:raise ValueError('series_pool_changed')
        row=request['row']
        if not isinstance(row,int) or row<0 or row>=len(self.corpus.ids):raise ValueError('invalid_series_row')
        query=self.corpus.ids[row];edges=[]
        self.route_delta[query]={'corpus':self.corpus_identity,'value':self.corpus.retrieve(query)}
        for other in self.corpus.retrieve(query)['candidates']:
            s=self.corpus.signal(query,other)
            if predict(s,PARAMETERS)!='none':edges.append(sorted((query,other)))
        return {'row':row,'next_row':row+1,'edges':sorted(edges)}

    def cluster(self,request):
        if request['pool_key']!=self.key:raise ValueError('series_pool_changed')
        edges=sorted(set(tuple(e) for e in request['edges']))
        if len(edges)>MAX_EDGES or any(len(e)!=2 or e[0]>=e[1] or any(i not in self.corpus.works for i in e) for e in edges):raise ValueError('invalid_series_evidence')
        edges=[e for e in edges if not self.blocked.intersection(e)]
        # Only validated candidates schedule merges; missing cross edges are scored.
        if any(predict(self.corpus.signal(*e),PARAMETERS)=='none' for e in edges):raise ValueError('series_evidence_mismatch')
        partition=agglomerate(self.ids,edges,self.corpus,PARAMETERS,'average',representative_subject,self.anchors)
        revised=boundary_review(partition['groups'],edges,self.corpus,PARAMETERS,'average',representative_subject)
        return {'algorithm_version':VERSION,'parameters':PARAMETERS,
                'proposals':proposals(revised['groups'],self.corpus,PARAMETERS,'average',representative_subject),
                'pending':revised['pending'],'unavailable_series':self.unavailable,'automatic_application':False}

    def validate(self,request):
        if request['pool_key']!=self.key:raise ValueError('series_pool_changed')
        ids=request['work_ids']
        if not isinstance(ids,list) or any(i not in self.corpus.works for i in ids):raise ValueError('invalid_series_selection')
        value=assess(ids,self.corpus,PARAMETERS,'average',representative_subject)
        if value is None:raise ValueError('series_selection_not_supported')
        return {'valid':True,'tier':value['tier'],'risks':value['risks'],'algorithm_version':VERSION}

    def restore(self,request):
        if request['pool_key']!=self.key:raise ValueError('series_pool_changed')
        records=request.get('signals')
        if records is None:records=[]
        routes=request.get('routes')
        if routes is None:routes={}
        if not isinstance(routes,dict):raise ValueError('invalid_series_checkpoint')
        if not isinstance(records,list) or len(records)>512:raise ValueError('series_restore_limit')
        for r in records:
            if not isinstance(r,dict) or any(not isinstance(r.get(k),str) for k in ('a','b','key')):raise ValueError('invalid_series_checkpoint')
            a,b=r['a'],r['b']
            if a>=b or a not in self.profiles or b not in self.profiles:continue
            if r['key']!=self.corpus.pair_key(a,b):continue
            if not isinstance(r.get('signal'),dict):raise ValueError('invalid_series_signal')
            if (a,b) in self.corpus.persistent_signals:continue
            self.corpus.persistent_signals[a,b]=r['signal'];self.corpus.signal_delta[a,b]=r
        for wid,r in routes.items():
            if not isinstance(wid,str) or not isinstance(r,dict):raise ValueError('invalid_series_checkpoint')
            if r.get('corpus')==self.corpus_identity and wid in self.profiles:
                value=r.get('value')
                if not isinstance(value,dict) or any(not isinstance(value.get(k),list) or any(not isinstance(i,str) or i not in self.profiles or i==wid for i in value[k]) for k in ('title','creator_title','semantic','candidates')):raise ValueError('invalid_series_checkpoint')
                self.corpus.routes[wid]=value;self.route_delta[wid]=r
        check_resources()
        return {'restored':len(records)}

    def checkpoint(self,request):
        if request['pool_key']!=self.key:raise ValueError('series_pool_changed')
        values=[]
        while self.corpus.signal_delta and len(values)<512:
            _,r=self.corpus.signal_delta.popitem(last=False);values.append(r)
        routes=self.route_delta;self.route_delta={}
        return {'signals':values,'routes':routes,'more':bool(self.corpus.signal_delta),
                'computed_pairs':len(self.corpus.computed_signal_keys),'reused_pairs':len(self.corpus.reused_signal_keys)}
