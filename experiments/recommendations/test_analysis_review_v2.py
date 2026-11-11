"""Anonymous report rendering checks; do not run or alter frozen scoring."""
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from review_analysis_v2 import category,render_reports


def row(label='same_work',action='defer',flag=False):
 return {'pair_id':'anonymous-pair','book_ids':[1,2],'label':label,'reason':'<script>anonymous</script>',
         'collection_or_partial_overlap':flag,'prediction':{'action':action,'scores':{'version':.8,'series':.5},'reasons':['insufficient_or_ambiguous_evidence']},
         'signals':{'scores':{'sscd':.7,'phash':.6,'minilm':.8},'title':.6,'series_title':.5,'pages':1.,'creator':{'states':{'artist':'missing'}},'explicit_difference':False,'ordinal_ambiguity':True}}


class ReadOnlyReviewTests(unittest.TestCase):
 def test_exclusions_and_defer_are_not_called_label_errors(self):
  self.assertEqual(category(row(flag=True)),'excluded')
  self.assertEqual(category(row(label='uncertain',action='same_work')),'excluded')
  self.assertEqual(category(row()),'defer')
  self.assertEqual(category(row(action='same_series')),'wrong')
  self.assertEqual(category(row(action='same_work')),'correct')

 def test_escaped_private_fields_no_annotation_export_and_static_script_valid(self):
  books={i:{'title':'</script><img onerror=anonymous()>','title_jpn':None,'cover_path':None,'pages':20,'language_code':'en','tags':[['artist','anonymous']]} for i in (1,2)}
  reports={'calibration':{'review_items':[row()]},'heldout':{'review_items':[row(label='same_series',action='same_work')]}}
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp).resolve();out=root/'review-results.html';summary=render_reports(reports,books,root,out,'anonymous-policy')
   text=out.read_text()
  self.assertEqual(summary['review_pairs'],2)
  self.assertNotIn(books[1]['title'],text)
  self.assertNotIn('<script>anonymous</script>',text)
  self.assertIn("connect-src 'none'",text)
  self.assertNotIn('createObjectURL',text)
  self.assertNotIn('localStorage',text)
  script=re.findall(r'<script>(.*?)</script>',text,re.S)
  self.assertEqual(len(script),1)
  result=subprocess.run(['node','--check'],input=script[0],text=True,capture_output=True)
  self.assertEqual(result.returncode,0,result.stderr)

 def test_render_preserves_original_report_rows(self):
  import copy
  reports={'heldout':{'review_items':[row(flag=True)]}};original=copy.deepcopy(reports)
  books={i:{'title':'Anonymous','title_jpn':None,'cover_path':None,'pages':20,'language_code':None,'tags':[]} for i in (1,2)}
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp).resolve();render_reports(reports,books,root,root/'review.html','anonymous')
  self.assertEqual(reports,original)


if __name__=='__main__':unittest.main()
