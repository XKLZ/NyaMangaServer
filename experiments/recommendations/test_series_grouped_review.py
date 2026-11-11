import json
import subprocess
import unittest
import series_delivery as delivery


class GroupedReviewTests(unittest.TestCase):
    def test_pagination_and_alternative_selection_preserve_full_plans(self):
        metric={'tp':1,'fp':0,'fn':0,'precision':1,'recall':1}
        proposals=[{'id':str(i),'kind':'create_series','subject':str(i//2),'work_ids':['w'],
                    'suggested_name':'Anonymous','tier':'strong','order':{'suggested_order':None},'risks':[]}
                   for i in range(18)]
        payload={'report':{'proposals':proposals,'virtual_series_scenarios':[],'diagnostics':[],
                          'forward_ranking':{'queries':[]},'baselines':{'v2':metric,'v3':metric},
                          'oof':{'strong':metric,'supplementary':metric,'combined':metric}},
                 'proposal_bundles':delivery.bundles(proposals),
                 'delivery_summary':{'known_positive_coverage_in_create_plans':{'found':1,'positive_pairs':2,'coverage':.5}},
                 'works':{'w':{'edition_ids':[1]}},
                 'books':{'1':{'image':None,'image_status':'Anonymous missing','title':'Anonymous',
                               'title_original':'','language':None,'pages':10,'tags':[]}}}
        harness='''const vm=require('node:vm');const created=[];class E{constructor(tag){this.tag=tag;this.children=[];this.value='';this.textContent='';created.push(this)}setAttribute(){}append(...items){this.children.push(...items);if(this.tag==='select'&&!this.value&&items[0]?.tag==='option')this.value=items[0].value}replaceChildren(){this.children=[]}}const es={};const document={createElement:t=>new E(t),getElementById:id=>es[id]??(es[id]=new E(id))};document.getElementById('payload').textContent=JSON.stringify(PAYLOAD);document.getElementById('mode').value='create';vm.runInNewContext(SCRIPT,{document});if(es.content.children.length!==8)throw Error('first page');const select=created.find(e=>e.tag==='select');if(select.children.length!==2)throw Error('alternatives lost');select.value=select.children[1].value;select.onchange();es.next.onclick();if(es.content.children.length!==1)throw Error('second page');es.previous.onclick();if(es.content.children.length!==8)throw Error('return page');'''
        harness=harness.replace('PAYLOAD',json.dumps(payload)).replace('SCRIPT',json.dumps(delivery.grouped_script()))
        result=subprocess.run(['node','-e',harness],capture_output=True)
        self.assertEqual(result.returncode,0,result.stderr.decode())


if __name__=='__main__':unittest.main()
