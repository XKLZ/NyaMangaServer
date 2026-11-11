import unittest
import numpy as np
from discovery import Discovery
from worker import validate_book
import matching

class DiscoveryTests(unittest.TestCase):
    def test_unordered_pair_once_and_canonical_thresholds(self):
        for threshold in (.65,.75):
            books=[{'id':i,'library_id':'x','title':'Anonymous','title_jpn':'','pages':10,'tags':[]} for i in ('a','b','c')]
            x=np.zeros(512,dtype=np.float32);x[0]=1
            y=x.copy();y[0]=threshold;y[1]=np.sqrt(1-threshold**2)
            vectors={'a':x.tolist(),'b':y.tolist(),'c':x.tolist()}
            d=Discovery({'books':books,'features':{'sscd':vectors},'pool_key':'k'},validate_book)
            # Force coarse BLAS above a gate; final scoring must use canonical dot.
            d.matrices['sscd'][1,0] += 1e-5
            rows=[d.row({'pool_key':'k','row':i}) for i in range(3)]
            self.assertEqual(sum(r['compared'] for r in rows),3)
            edges={(e['a'],e['b']):e for r in rows for e in r['edges']}
            expected=matching.pair_signal('a','b',d.books,d.parsed,d.features,matching.V2_PARAMETERS)
            tier=matching.decision(expected,matching.PARAMETERS)['tier']
            self.assertEqual(edges.get((0,1),{}).get('tier','none'),tier)
            if tier!='none':self.assertEqual(edges[0,1]['score'],expected['sscd'])
    def test_pool_budget_and_key(self):
        books=[{'id':str(i),'library_id':'x','title':'','tags':[]} for i in range(1001)]
        d=Discovery({'books':books,'features':{},'pool_key':'k'},validate_book)
        self.assertEqual(len(d.parsed),1001)
        with self.assertRaises(ValueError):d.row({'pool_key':'other','row':0})
        with self.assertRaises(ValueError):Discovery({'books':books*7,'features':{},'pool_key':'k'},validate_book)


class IncrementalRowsTests(unittest.TestCase):
    def test_selected_pairs_equal_full_rows_and_do_not_score_reused_pairs(self):
        from unittest.mock import patch
        books=[{'id':i,'library_id':'x','title':'Anonymous','title_jpn':'','pages':10,'tags':[]} for i in ('a','b','c','d')]
        vectors={}
        for i, book in enumerate(books):
            x=np.zeros(512,dtype=np.float32); x[0]=1-i*.1; x[1]=np.sqrt(1-x[0]**2); vectors[book['id']]=x.tolist()
        d=Discovery({'books':books,'features':{'sscd':vectors},'pool_key':'k'},validate_book)
        full=d.row({'pool_key':'k','row':0})
        with patch.object(matching,'pair_signal',wraps=matching.pair_signal) as score:
            partial=d.row({'pool_key':'k','row':0,'targets':[2]})
            self.assertEqual(score.call_count,1)
        self.assertEqual(partial['compared'],1)
        self.assertEqual(partial['edges'],[e for e in full['edges'] if e['b']==2])
        with patch.object(matching,'pair_signal',side_effect=AssertionError('recomputed')):
            self.assertEqual(d.row({'pool_key':'k','row':0,'targets':[]})['compared'],0)
        for targets in ([0],[2,2],[4],[-1],[True]):
            with self.assertRaises(ValueError):d.row({'pool_key':'k','row':0,'targets':targets})

if __name__=='__main__':unittest.main()
