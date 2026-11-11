import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import series2_review as review


def payload():
    metric={'tp':1,'fp':0,'fn':1,'precision':1.,'recall':.5}
    audit={'primary_groups':9,'pending_works':0,'alternative_count':0,'duplicate_memberships':0,
           'pair_metrics':{'combined':metric},'predictions':{}}
    proposals=[{'id':str(i),'work_ids':['a','b'],'tier':'strong','suggested_name':'Anonymous',
                'risks':[],'order':{'suggested_order':None}} for i in range(9)]
    report={'config':{'default':'average_profile'},'fullfit':{n:{'proposals':proposals,'pending':[],
             'audit':audit,'before_boundary_audit':audit} for n in review.run.VARIANTS},
             'oof':{n:{'metrics':{'combined':metric,'strong':metric},'before_boundary_metrics':{'combined':metric}} for n in review.run.VARIANTS},
             'virtual_series':[],'prior_v1':{'raw_proposals':20,'fullfit_group_pair_metrics':metric},'resources':{}}
    books={str(i):{'title':'Anonymous','title_original':'','pages':10,'language':None,'tags':[], 'image':None} for i in range(29)}
    return {'report':report,'summary':review.summary(report),'rows':[],
             'works':{'a':{'edition_ids':[0]},'b':{'edition_ids':[1]},
                      **{str(i):{'edition_ids':[i]} for i in range(29)}},'books':books}


HARNESS='''const vm=require('node:vm');const created=[];
class E{constructor(tag){this.tag=tag;this.children=[];this.value='';this.textContent='';created.push(this)}
append(...items){this.children.push(...items)}replaceChildren(...items){this.children=[...items]}}
const es={};const document={createElement:t=>new E(t),getElementById:id=>es[id]??(es[id]=new E(id))};
document.getElementById('payload').textContent=JSON.stringify(PAYLOAD);
document.getElementById('variant').value='average_profile';document.getElementById('mode').value='primary';
vm.runInNewContext(SCRIPT+'\\n'+CHECK,{document});'''


class ReviewTests(unittest.TestCase):
    def javascript(self,check):
        source=Path(review.__file__).with_suffix('.js').read_text()
        harness=HARNESS.replace('PAYLOAD',json.dumps(payload())).replace('SCRIPT',json.dumps(source)).replace('CHECK',json.dumps(check))
        r=subprocess.run(['node','-e',harness],capture_output=True,text=True)
        self.assertEqual(r.returncode,0,r.stderr)

    def test_page_cannot_execute_private_titles_and_hash_matches_script(self):
        p=payload();p['books']['0']['title']='</script><img src=https://invalid> __JAVASCRIPT__'
        html=review.page(p)
        found=re.search(r'<script id="payload" type="application/json">(.*?)</script>',html,re.S)
        self.assertNotIn('</script>',found[1]);self.assertEqual(json.loads(found[1])['books']['0']['title'],p['books']['0']['title'])
        source=Path(review.__file__).with_suffix('.js').read_bytes()
        sha='sha256-'+base64.b64encode(hashlib.sha256(source).digest()).decode()
        self.assertIn("script-src '"+sha+"'",html)
        self.assertIn("connect-src 'none'",html)

    def test_no_network_dynamic_html_or_label_persistence_and_syntax_valid(self):
        script=Path(review.__file__).with_suffix('.js')
        for token in ('fetch(', 'XMLHttpRequest','innerHTML','localStorage','eval('):self.assertNotIn(token,script.read_text())
        r=subprocess.run(['node','--check',str(script)],capture_output=True,text=True)
        self.assertEqual(r.returncode,0,r.stderr)

    def test_list_pagination_variant_switch_and_all_modes(self):
        self.javascript("""if($('content').children.length!==8)throw Error('first page');
        $('next').onclick();if($('content').children.length!==1)throw Error('next page');
        $('previous').onclick();if($('content').children.length!==8)throw Error('previous page');
        for(const variant of Object.keys(R.fullfit)){$('variant').value=variant;$('variant').onchange();}
        for(const mode of ['pending','join','comparison','cases','primary']){$('mode').value=mode;$('mode').onchange();}
        if(!$('progress').textContent.includes('9 项'))throw Error('count');""")

    def test_full_group_members_are_accessible_in_batches(self):
        self.javascript("""const box=members(Array.from({length:29},(_,i)=>String(i)));
        const tools=box.children[0],grid=box.children[1];
        if(grid.children.length!==12)throw Error('member first batch');
        tools.children[1].onclick();if(grid.children.length!==12)throw Error('member second batch');
        tools.children[1].onclick();if(grid.children.length!==5)throw Error('member last batch');
        if(!tools.children[1].disabled)throw Error('member end');
        tools.children[0].onclick();if(grid.children.length!==12)throw Error('member previous');""")

    def test_pending_options_and_empty_choices_are_readable(self):
        self.javascript("""const a=pendingCard({work_id:'a',reason:'multiple_series_targets_similarly_supported',options:[],qualified_targets:0});
        if(a.children.length<4)throw Error('pending empty');
        const b=pendingCard({work_id:'b',reason:'other',qualified_targets:1,options:[{target_work_ids:['a'],kind:'create_series',risks:[],order:{suggested_order:null}}]});
        if(b.children.length<4)throw Error('pending target');""")

    def test_cover_lookup_rejects_unapproved_fields_and_missing_source(self):
        book={'id':1,'title':'Anonymous','title_jpn':'','tags':[],'cover_path':'/unapproved/private/file.jpg'}
        evidence={'works':{'a':{'edition_ids':[1]}},'rows':[{'work_ids':['a']}]}
        report={'fullfit':{},'virtual_series':[]}
        def read(path):return evidence if Path(path).name=='evidence.json' else {'books':[book]}
        with tempfile.TemporaryDirectory() as d,patch.object(review.run,'read',side_effect=read):
            display,audit=review.material(report,Path(d).resolve())
            self.assertIsNone(display['books']['1']['image']);self.assertEqual(audit['missing_covers'],1)
        def changed(path):
            if Path(path).name=='evidence.json':return evidence
            return {'books':[{**book,'title':'changed'}]} if Path(path).is_relative_to(review.run.data.old.SOURCE) else {'books':[book]}
        with tempfile.TemporaryDirectory() as d,patch.object(review.run,'read',side_effect=changed):
            with self.assertRaises(ValueError):review.material(report,Path(d).resolve())


if __name__=='__main__':unittest.main()
