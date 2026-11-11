"""Whole-library pair execution: one parsed pool, bounded row blocks and no inference."""
import base64
import numpy as np
from common import digest
from features import VERSIONS, vector, check_resources
import matching

VERSION = 'work-discovery-pairs-v1'
MAX_COMPARISONS = 20_000_000

class Discovery:
    def __init__(self, request, validate_book):
        items = request['books']
        if not isinstance(items, list) or not items or len(items)*(len(items)-1)//2 > MAX_COMPARISONS:
            raise ValueError('discovery_comparison_budget')
        for book in items: validate_book(book)
        self.books = {book['id']:book for book in items}
        if len(self.books) != len(items) or len({book.get('library_id') for book in items}) != 1:
            raise ValueError('invalid_discovery_pool')
        self.ids = sorted(self.books)
        self.key = request['pool_key']
        self.hashes = {i:digest(self.books[i]) for i in self.ids}
        self.parsed = matching.structures(self.books)
        self.features = {a:{} for a in VERSIONS}
        for algorithm, rows in request.get('features',{}).items():
            if algorithm not in VERSIONS: raise ValueError('invalid_algorithm')
            for i,value in rows.items():
                if i not in self.books: raise ValueError('unknown_feature_edition')
                if algorithm == 'phash':
                    if not isinstance(value,str) or len(value)!=16: raise ValueError('invalid_phash')
                    self.features[algorithm][i]=int(value,16)
                else:
                    if isinstance(value,dict):
                        if set(value)!={'encoding','data'} or value['encoding']!='f32le':raise ValueError('invalid_packed_vector')
                        value=np.frombuffer(base64.b64decode(value['data'],validate=True),dtype='<f4')
                    self.features[algorithm][i]=vector(value,algorithm)
        self.matrices = {a:np.stack([self.features[a].get(i,np.zeros(512 if a=='sscd' else 384,dtype=np.float32)) for i in self.ids]) for a in ('sscd','minilm')}
        check_resources()

    def row(self,request):
        if request['pool_key']!=self.key:raise ValueError('discovery_pool_changed')
        index=request['row']
        if not isinstance(index,int) or index<0 or index>=len(self.ids):raise ValueError('invalid_pair_row')
        query=self.ids[index];tail=self.ids[index+1:]
        targets=request.get('targets',list(range(index+1,len(self.ids))))
        if not isinstance(targets,list) or len(set(targets))!=len(targets) or any(type(i) is not int or i<=index or i>=len(self.ids) for i in targets):
            raise ValueError('invalid_pair_targets')
        selected=np.asarray(targets,dtype=np.intp)
        # Single-threaded BLAS computes a bounded row, never an N-by-N float matrix.
        products={a:self.matrices[a][selected] @ self.matrices[a][index] for a in self.matrices}
        hard=bytearray((len(tail)+7)//8);edges=[]
        for offset,target in enumerate(targets):
            position=target-index-1;other=self.ids[target]
            scores={}
            for a in VERSIONS:
                aa=self.features[a].get(query);bb=self.features[a].get(other)
                if aa is None or bb is None:scores[a]=None
                elif a=='phash':scores[a]=1-(aa^bb).bit_count()/63
                else:scores[a]=max(-1.,min(1.,float(products[a][offset])))
            signal=matching.pair_signal(query,other,self.books,self.parsed,self.features,matching.V2_PARAMETERS,scores,digest([self.hashes[query],self.hashes[other]]))
            decision=matching.decision(signal,matching.PARAMETERS)
            if matching.HARD_RISKS.intersection(signal['risks']):hard[position//8]|=1<<(position%8)
            if decision['tier']!='none':edges.append({'a':index,'b':index+1+position,'tier':decision['tier'],'score':signal['sscd'],'signal':signal})
        check_resources()
        return {'row':index,'next_row':index+1,'compared':len(targets),'hard_bits':base64.b64encode(hard).decode(),'edges':edges}
