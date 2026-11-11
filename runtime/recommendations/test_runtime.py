"""Anonymous production worker invariants; no network or private sample inputs."""
import base64
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import numpy as np
from PIL import Image
import common
import features
import labels
import matching
import worker


def book(i, title='Example', **changes):
    return {'id': i, 'library_id': 'anon', 'title': title, 'title_jpn': '', 'pages': 12, 'tags': [['artist', 'Anon']], **changes}


def embedding(size, axis=0):
    value = [0.] * size
    value[axis] = 1.
    return value


class RuntimeTests(unittest.TestCase):
    def test_vectors_not_renormalized_and_negative_valid(self):
        v = np.asarray(embedding(512), dtype=np.float32)
        self.assertEqual(features.vector(v.tolist(), 'sscd').tobytes(), v.tobytes())
        v[0] = -1
        self.assertEqual(features.vector(v.tolist(), 'sscd')[0], -1)
        with self.assertRaises(ValueError): features.vector([0.] * 512, 'sscd')

    def test_bounded_image_and_first_frame(self):
        image = Image.new('RGB', (12, 12), 'red')
        raw = io.BytesIO(); image.save(raw, 'BMP')
        self.assertEqual(features.image_from_bytes(raw.getvalue()).size, (12, 12))
        for value in (b'', b'broken', b'x' * (10 * 1024**2 + 1)):
            with self.assertRaises((ValueError, OSError)): features.image_from_bytes(value)

    def test_private_cache_digest_and_no_model_load_on_hit(self):
        with tempfile.TemporaryDirectory() as d:
            runtime = features.Runtime(Path(d).resolve()/'models', Path(d).resolve()/'cache')
            image = Image.new('RGB', (12, 12), 'red'); raw = io.BytesIO(); image.save(raw, 'PNG')
            first = runtime.feature('phash', raw.getvalue())
            self.assertEqual(runtime.stats['computed_inputs'], 1)
            with patch.object(runtime, 'load', side_effect=AssertionError('cache loaded model')):
                self.assertEqual(first, runtime.feature('phash', raw.getvalue()))
            path = runtime.cache/'phash'/(first['fingerprint']+'.json')
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)
            item=json.loads(path.read_text());item['feature']='0'*16;path.write_text(json.dumps(item))
            with self.assertRaisesRegex(ValueError, 'cache_corrupt'): runtime.feature('phash', raw.getvalue())

    def test_verified_cache_read_and_corrupt_repair_are_offline(self):
        with tempfile.TemporaryDirectory() as d:
            runtime = features.Runtime(Path(d).resolve()/'models', Path(d).resolve()/'cache')
            image = Image.new('RGB', (12, 12), 'red'); raw = io.BytesIO(); image.save(raw,'PNG')
            first = runtime.feature('phash',raw.getvalue())
            with patch.object(runtime,'load',side_effect=AssertionError('verified cache inferred')):
                self.assertEqual(first,runtime.cached_feature('phash',first['fingerprint']))
                self.assertIsNone(runtime.cached_feature('phash','a'*64))
            path=runtime.cache/'phash'/(first['fingerprint']+'.json')
            path.write_text('{}')
            self.assertIsNone(runtime.cached_feature('phash',first['fingerprint']))
            self.assertEqual(first,runtime.feature('phash',raw.getvalue(),repair=True))
            self.assertEqual(runtime.stats['computed_inputs'],2)

    def test_missing_model_is_capability_failure(self):
        with tempfile.TemporaryDirectory() as d:
            runtime = features.Runtime(Path(d).resolve()/'missing', Path(d).resolve()/'cache')
            with self.assertRaisesRegex(ValueError, 'model_missing'): runtime.capabilities()
            self.assertEqual(runtime.stats['model_loads'], 0)

    def test_cache_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/'link').symlink_to(root/'target')
            with self.assertRaises(ValueError): features.Runtime(root, root/'link'/'cache')

    def test_same_work_de_dupe_nonpreferred_and_conflicts(self):
        request={'books':[book('q'),book('a', 'Different'),book('b')], 'query_edition_id':'q', 'works':[{'id':'w', 'edition_ids':['a','b'], 'preferred_edition_id':'a'}], 'features':{'sscd':{'q':embedding(512), 'a':embedding(512,1), 'b':embedding(512)}}}
        out=worker.recommend(request)
        self.assertEqual(len(out['items']),1)
        self.assertEqual(out['items'][0]['supporting_edition_id'],'b')
        self.assertEqual(out['items'][0]['tier'],'strong')
        request['books'][1]['tags']=[['artist','Other']]
        self.assertEqual(worker.recommend(request)['items'][0]['tier'],'supplementary')

    def test_self_work_excluded_and_library_checked(self):
        request={'books':[book('q'),book('a')], 'query_edition_id':'q', 'works':[{'id':'w', 'edition_ids':['q','a'], 'preferred_edition_id':'q'}]}
        self.assertEqual(worker.recommend(request)['items'],[])
        request['works'][0]['edition_ids']=['a'];request['works'][0]['preferred_edition_id']='a';request['books'][1]['library_id']='other'
        with self.assertRaisesRegex(ValueError,'cross_library'):worker.recommend(request)

    def test_packed_float32_wire(self):
        value=np.asarray(embedding(512),dtype='<f4')
        encoded={'encoding':'f32le','data':base64.b64encode(value.tobytes()).decode()}
        request={'books':[book('q'),book('a')], 'query_edition_id':'q', 'works':[{'id':'w','edition_ids':['a'],'preferred_edition_id':'a'}], 'features':{'sscd':{'q':encoded,'a':encoded}}}
        self.assertEqual(worker.recommend(request)['items'][0]['tier'],'strong')
        encoded['encoding']='f64'
        with self.assertRaises(ValueError):worker.recommend(request)

    def test_content_conflict_supplementary_not_discarded(self):
        value={'sscd':.99,'fixed_v2_same_work':False,'risks':['different_explicit_volume_or_chapter']}
        self.assertEqual(matching.decision(value,matching.PARAMETERS)['tier'],'supplementary')
        value['sscd']=.6
        self.assertEqual(matching.decision(value,matching.PARAMETERS)['tier'],'none')

    def test_custom_labels_not_inference_or_matching(self):
        a=book('a',custom_labels=['彩色','私有'])
        b=book('b')
        value=labels.suggest_work([a,b])
        self.assertEqual(value['editions'][0]['existing_custom_labels'],['彩色','私有'])
        self.assertEqual(value['editions'][0]['suggested_presets'],[])
        self.assertEqual(labels.suggest_work([a])['editions'][0]['suggested_presets'],[])

    def test_language_does_not_suppress_reliable_properties(self):
        a=book('a',tags=[['language','translated'],['language','chinese']])
        b=book('b',tags=[['language','japanese']])
        self.assertEqual(labels.suggest_work([a,b])['editions'][0]['suggested_presets'],[{'id':'translated','label':'译本'}])
        inferred=labels.infer(book('a',is_full_color=False,is_translated=False,censorship='censored'))
        self.assertTrue(all(p['value'] is None for p in inferred['properties'].values()))
        a=book('a', manual_properties={'color':{'value':'color'}})
        b=book('b', manual_properties={'color':{'value':'monochrome'}})
        self.assertEqual(labels.suggest_work([a,b])['editions'][0]['suggested_presets'],[{'id':'color','label':'彩色'}])

    def test_protocol_rejects_arbitrary_operation_and_invalid_books(self):
        with self.assertRaises(ValueError): worker.dispatch(None,{'operation':'capabilities'})
        with self.assertRaises(ValueError): worker.dispatch(None,{'protocol':worker.PROTOCOL,'operation':'download'})
        with self.assertRaises(ValueError): worker.validate_book(book('a',tags=[['artist']]))
        with self.assertRaises(ValueError): worker.validate_book(book('a',pages=-1))


if __name__ == '__main__': unittest.main()
