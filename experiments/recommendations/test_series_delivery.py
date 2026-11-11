import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import core
import series_data as data
import series_run as run
import series_review as review
import series_delivery as delivery
from series_proposals import join_plan
from test_series_match import SeriesMatchTests


class SeriesDeliveryTests(unittest.TestCase):
    def test_grouped_review_keeps_every_alternative_without_merging(self):
        rows=[{'id':str(i),'subject':'anonymous','tier':'strong','work_ids':['a',str(i)]} for i in range(9)]
        b=delivery.bundles(rows)
        self.assertEqual(len(b),1)
        self.assertEqual(set(b[0]['proposal_ids']),set(str(i) for i in range(9)))
        self.assertIn('D.proposal_bundles',delivery.grouped_script())

    def test_join_checks_conflicts_on_non_supporting_member(self):
        corpus,m=SeriesMatchTests().corpus()
        original=corpus.signal
        def signal(a,b):
            s=original(a,b).copy()
            s.update(eligible=True,raw=.9,dual=.9)
            s['risks']=['explicit_creator_conflict'] if m[2] in (a,b) else []
            return s
        corpus.signal=signal
        p=join_plan(corpus,m[3],{'id':'series','name':'Anonymous','work_ids':[m[1],m[2]]},
                    {'strategy':'raw','strong':.8,'supplementary':.5})
        self.assertEqual(p['tier'],'supplementary')
        self.assertIn('explicit_creator_conflict',p['risks'])
    def test_private_json_cannot_end_script_or_become_template(self):
        raw = review.safe_json({'title':'</script><img src=https://invalid> __JAVASCRIPT__ & \u2028'})
        self.assertNotIn('</script>',raw)
        self.assertEqual(json.loads(raw)['title'],'</script><img src=https://invalid> __JAVASCRIPT__ & \u2028')
        self.assertIn("connect-src 'none'",review.PAGE)
        script=Path(review.__file__).with_suffix('.js').read_text()
        self.assertNotIn('innerHTML',script)
        self.assertNotIn('fetch(',script)
        self.assertNotIn('localStorage',script)
        result=subprocess.run(['node','--check',str(Path(review.__file__).with_suffix('.js'))],capture_output=True)
        self.assertEqual(result.returncode,0,result.stderr.decode())

    def test_atomic_publication_cancel_never_activates_partial(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d).resolve()
            with patch.object(data,'OUTPUT',root):
                target=root/'final'
                with self.assertRaises(InterruptedError):
                    with run.publication(target) as pending:
                        core.atomic_json(pending/'part.json',{'anonymous':True})
                        raise InterruptedError('anonymous cancellation')
                self.assertFalse(target.exists());self.assertEqual(list(root.iterdir()),[])

    def test_manifest_verify_rejects_changes_and_does_not_rescore(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d).resolve()
            with patch.object(data,'OUTPUT',root):
                directory=data.private_dir(root/'final')
                core.atomic_json(directory/'report.json',{'anonymous':True})
                run.manifest(directory)
                self.assertEqual(len(run.verify(directory)['files']),1)
                (directory/'report.json').write_text('{}')
                with self.assertRaises(ValueError):run.verify(directory)

    def test_local_review_pagination_and_group_modes_with_fake_dom(self):
        script=Path(review.__file__).with_suffix('.js').read_text()
        metric={'tp':1,'fp':0,'fn':0,'precision':1,'recall':1}
        payload={'report':{'proposals':[],'virtual_series_scenarios':[],'diagnostics':[],
                          'forward_ranking':{'queries':[]},'baselines':{'v2':metric,'v3':metric},
                          'oof':{'strong':metric,'supplementary':metric,'combined':metric}},'works':{},'books':{}}
        harness='''const vm=require('node:vm');class E{constructor(tag){this.tag=tag;this.children=[];this.value='create';this.textContent='';}append(...x){this.children.push(...x)}replaceChildren(){this.children=[]}}const es={};const document={createElement:t=>new E(t),getElementById:id=>es[id]??(es[id]=new E(id))};document.getElementById('payload').textContent=JSON.stringify(PAYLOAD);vm.runInNewContext(SCRIPT,{document});for(const mode of ['create','join','queries','errors']){es.mode.value=mode;es.mode.onchange();if(!es.progress.textContent.includes('0 项'))throw Error('count mismatch')}if(!es.previous.disabled||!es.next.disabled)throw Error('pagination');'''
        harness=harness.replace('PAYLOAD',json.dumps(payload)).replace('SCRIPT',json.dumps(script))
        result=subprocess.run(['node','-e',harness],capture_output=True)
        self.assertEqual(result.returncode,0,result.stderr.decode())


if __name__=='__main__':unittest.main()
