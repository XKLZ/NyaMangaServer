"""Only anonymous synthetic inputs; never opens the private fixture."""
import collections
import contextlib
import json
import io
from pathlib import Path
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch
import core
import features
import review
import models


def synthetic_books():
    books = {}
    for family in range(100):
        name = core.digest(["anonymous", family])[:24]
        for member in range(4):
            i = family * 4 + member + 1
            books[i] = {"id": i, "title": name + " vol " + str(member // 2 + 1), "title_jpn": None,
                        "version_group_id": family * 2 + member // 2 + 1, "is_primary_version": member % 2 == 0,
                        "collection_id": family + 1, "collection_order": member, "pages": 20,
                        "cover_path": None, "cover_hash": None, "gallery_id_sampling_only": family + 1,
                        "language_code": "en", "tags": [["artist", "Anonymous creator " + str(family % 5)]]}
    return books


class RulesTests(unittest.TestCase):
    def test_preserves_intrinsic_numbers_and_unknown_brackets(self):
        for title, expected in (("Agent 007", "agent007"), ("3×3 Eyes", "33eyes"), ("[Secret Garden]", "secretgarden"), ("(The English Patient)", "theenglishpatient")):
            self.assertEqual(core.title_structure(title)["core"], expected)
        self.assertEqual(core.title_structure("[English] Agent 007 Vol. 2")["core"], "agent007")

    def test_ordinal_type_and_cross_volume_block(self):
        self.assertEqual(core.title_structure("Anonymous vol1")["volumes"], [["volume", 1]])
        self.assertEqual(core.title_structure("Anonymous 第1話")["volumes"], [["chapter", 1]])
        books = synthetic_books()
        self.assertTrue(core.rules(books[1], books[3])["version_blocked"])
        self.assertFalse(core.rules(books[1], books[2])["version_blocked"])
        books[1]["title"] = "Anonymous vol1"
        books[2]["title"] = "Anonymous chapter1"
        self.assertTrue(core.rules(books[1], books[2])["version_blocked"])

    def test_gallery_is_never_identity_evidence(self):
        books = synthetic_books()
        before = core.rules(books[1], books[5])
        books[5]["gallery_id_sampling_only"] = books[1]["gallery_id_sampling_only"]
        self.assertEqual(before, core.rules(books[1], books[5]))

    def test_70_30_isolation_and_logical_series_sampling(self):
        books = synthetic_books()
        pairs, isolation = core.select_pairs(books)
        self.assertEqual(len(pairs), 80)
        self.assertEqual(isolation["known_family_overlap"], 0)
        self.assertEqual(collections.Counter(p["split"] for p in pairs), {"calibration": 56, "test": 24})
        for task in ("version", "series"):
            self.assertEqual(collections.Counter(p["stratum"] for p in pairs if p["task"] == task), {"positive_candidate": 20, "hard_negative_candidate": 15, "ordinary_negative_candidate": 5})
        for p in pairs:
            if p["task"] == "series" and p["stratum"] == "positive_candidate":
                a, b = [books[i] for i in p["book_ids"]]
                self.assertNotEqual(a["version_group_id"], b["version_group_id"])
        cal = {f for p in pairs if p["split"] == "calibration" for f in p["family_ids"]}
        test = {f for p in pairs if p["split"] == "test" for f in p["family_ids"]}
        self.assertFalse(cal & test)
        self.assertEqual(pairs, core.select_pairs(books)[0])


class BoundaryTests(unittest.TestCase):
    def test_failed_conversion_discards_only_unpublished_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp).resolve() / 'model.onnx';target.write_bytes(b'old verified model')
            with self.assertRaises(ValueError):
                with models.temporary_model(target) as pending:
                    pending.write_bytes(b'unverified model')
                    raise ValueError('anonymous conversion failure')
            self.assertEqual(target.read_bytes(), b'old verified model')
            self.assertEqual(list(target.parent.iterdir()), [target])
    def test_cover_mapping_never_dereferences_original_prefix(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();(root / "Covers/ab").mkdir(parents=True)
            name = "ab" + "0" * 38 + ".jpg"
            p = root / "Covers/ab" / name;p.write_bytes(b"anonymous")
            self.assertEqual(core.cover_file(root, "/does-not-exist/Covers/ab/" + name), p)
            with self.assertRaises(ValueError):
                core.cover_file(root, "/Covers/ab/../" + name)
            p.unlink();p.symlink_to(root / "outside")
            with self.assertRaises((ValueError, FileNotFoundError)):
                core.cover_file(root, "/Covers/ab/" + name)

    def test_output_escape_and_symlink_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();(root / "safe").mkdir();(root / "alias").symlink_to(root / "safe")
            with patch.object(core, "RUNTIME", root):
                with self.assertRaises(ValueError):core.private_output(root / "alias")
                with self.assertRaises(ValueError):core.private_output(root.parent / "escape")

    def test_cancel_does_not_publish_stale_result(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve() / "result.json";core.atomic_json(path, {"generation": 1})
            with patch.object(core, "CANCELLED", True):
                with self.assertRaises(InterruptedError):core.atomic_json(path, {"generation": 2})
            self.assertEqual(json.loads(path.read_text()), {"generation": 1})
            self.assertEqual(len(list(Path(tmp).resolve().iterdir())), 1)
            core.atomic_json(path, {"generation": 2})
            self.assertEqual(json.loads(path.read_text())["generation"], 2)

    def test_single_worker_ownership(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            with features.worker_lock(root):
                with self.assertRaises(ValueError):
                    with features.worker_lock(root):pass
            with features.worker_lock(root):pass


class AnnotationTests(unittest.TestCase):
    pack = {"pack_id": "anonymous-pack", "pairs": [{"pair_id": "anonymous-pair"}]}

    def valid(self):
        return {"schema": "recommendation-labels-v1", "pack_id": "anonymous-pack", "annotations": [{"pair_id": "anonymous-pair", "label": "uncertain", "reason": "Anonymous evidence insufficient"}]}

    def test_schema_missing_rows_invalid_types_and_duplicate_rejected(self):
        for value in (None, [], {}, {"pack_id": "anonymous-pack"}, {"schema": "recommendation-labels-v1", "pack_id": "anonymous-pack", "annotations": {}}, {**self.valid(), "annotations": [None]}, {**self.valid(), "annotations": self.valid()["annotations"] * 2}):
            with self.subTest(value=value), self.assertRaises(ValueError):core.validate_labels(self.pack, value)
        self.assertEqual(core.validate_labels(self.pack, self.valid()), self.valid())

    def test_empty_or_uncertain_is_not_ground_truth(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(ValueError):features.evaluate(Path(tmp).resolve(), None)

    def test_evaluation_counts_missing_features_and_actual_negative_strata(self):
        pairs = [{'pair_id':'a','task':'version','book_ids':[1,2],'stratum':'positive_candidate'},
                 {'pair_id':'b','task':'version','book_ids':[3,4],'stratum':'positive_candidate'},
                 {'pair_id':'c','task':'version','book_ids':[5,6],'stratum':'hard_negative_candidate'}]
        pack = {'pack_id':'anonymous','pairs':pairs}
        annotations = [{'pair_id':p['pair_id'],'label':'different' if p['pair_id']=='c' else 'same','reason':''} for p in pairs]
        rows = [{**p,'rules':{'version_score':0.1,'version_blocked':False,'reasons':[]},'scores':({'phash':0.5} if p['pair_id']!='b' else {})} for p in pairs]
        scores = {'pack_id':'anonymous','rule_version':core.RULE_VERSION,'rows':rows,'union_candidates':[{'task':'version','book_ids':[1,2],'routes':['phash']}]}
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp).resolve()
            (out/'labels-reviewed.json').write_text(json.dumps({'schema':'recommendation-labels-v1','pack_id':'anonymous','annotations':annotations}))
            (out/'scores.json').write_text(json.dumps(scores))
            with patch.object(features,'load_pack',return_value=(pack,{},list(range(1,7)))), patch.object(review,'render_diagnostics'), contextlib.redirect_stdout(io.StringIO()):
                features.evaluate(out,None)
            result=json.loads((out/'human-labeled-pilot.json').read_text())
            value=result['known_positive_sample_top5']['version:phash']
            self.assertEqual((value['labeled_same'],value['found'],value['feature_available'],value['feature_missing'],value['pool_books']),(2,1,1,1,6))
            self.assertFalse(result['sampling_bias']['all_decisive_different_from_ordinary_stratum'])

    def test_reason_limit_and_unknown_label(self):
        for field, value in (("reason", "x" * 4001), ("reason", None), ("label", "positive"), ("pair_id", "unknown"), ("pair_id", [])):
            labels = self.valid();labels["annotations"][0][field] = value
            with self.assertRaises(ValueError):core.validate_labels(self.pack, labels)

    def test_html_escapes_title_and_hides_sampling_hints(self):
        books = synthetic_books();books[1]["title"] = "</script><img src=x onerror=alert(1)>"
        pack = {"pack_id": "anonymous-pack", "pairs": [{"pair_id": "anonymous-pair", "task": "version", "book_ids": [1, 2]}]}
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp).resolve() / "review.html";review.render(pack, books, Path(tmp).resolve(), output)
            text = output.read_text()
            self.assertNotIn(books[1]["title"], text)
            self.assertIn("reason.maxLength=4000", text)
            self.assertIn("out.schema!==", text)
            self.assertIn("validState", text)
            self.assertIn("connect-src 'none'", text)

    def test_diagnostic_subset_is_read_only_and_preserves_original_protocol(self):
        books = synthetic_books();pair = {"pair_id": "anonymous-pair", "task": "version", "book_ids": [1, 2]}
        pack = {"pairs": [pair]};annotations = {'anonymous-pair': {'label':'same','reason':''}}
        scored = [{**pair, 'rules':{'version_score':0.1,'reasons':[]},'scores':{'sscd':0.01}}]
        diagnostic = [{'pair_id':'anonymous-pair','algorithm':'sscd','kind':'human_same_outside_route_top5','score':0.01}]
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp).resolve()/'outliers.html'
            review.render_diagnostics(pack, books, Path(tmp).resolve(), annotations, scored, diagnostic, output)
            text = output.read_text()
            self.assertIn('当前人工标注',text);self.assertIn('review.html',text)
            self.assertNotIn('<script>',text);self.assertNotIn('<input',text);self.assertNotIn('<textarea',text)


class ImageTests(unittest.TestCase):
    def test_phash_identical_and_corrupt_image(self):
        from PIL import Image, ImageDraw
        image = Image.new("RGB", (128, 96), "white")
        draw = ImageDraw.Draw(image);draw.rectangle((20, 30, 70, 80), fill="black")
        a = features.phash(image);b = features.phash(image.copy())
        self.assertEqual(features.phash_similarity(a, b), 1.0)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve() / "bad.jpg";path.write_bytes(b"not an image")
            with self.assertRaises(OSError):features.safe_image(path)

    def test_pixel_budget_before_decode(self):
        from PIL import Image
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve() / "synthetic.png"
            Image.new("1", (5000, 5000)).save(path)
            with self.assertRaises((ValueError, Warning)):features.safe_image(path)


class CacheTests(unittest.TestCase):
    def test_resume_and_all_fingerprint_boundaries(self):
        from PIL import Image
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();source = root / "source";output = root / "run"
            output.mkdir();books = {};calls = []
            for i in (1, 2, 3):
                name = f"{i:040x}.jpg";p = source / "Covers" / name[:2] / name
                p.parent.mkdir(parents=True, exist_ok=True)
                Image.new("RGB", (8, 8), (i * 50, 10, 20)).save(p)
                books[i] = {"id": i, "cover_path": "/old/Covers/" + name[:2] + "/" + name}
            pack = {"pack_id": "anonymous", "source": {"input_fingerprint": "anonymous-source"}}
            args = SimpleNamespace(algorithm="phash", limit=0)
            def infer(image):
                calls.append(1)
                return "0000000000000000"
            def cancelled(image):
                if len(calls) == 1:raise InterruptedError("anonymous cancellation")
                return infer(image)
            with patch.object(features, "INPUT", source), patch.object(features, "load_pack", return_value=(pack, books, [1, 2, 3])), patch.object(features, "model_worker", return_value=(cancelled, "model-a")), contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(InterruptedError):features.run(output, args)
                self.assertEqual(len(list((output / "feature-cache/phash").glob('*.json'))), 1)
            with patch.object(features, "INPUT", source), patch.object(features, "load_pack", return_value=(pack, books, [1, 2, 3])), patch.object(features, "model_worker", return_value=(infer, "model-a")), contextlib.redirect_stdout(io.StringIO()):
                features.run(output, args)
                self.assertEqual(len(calls), 3)
                features.run(output, args)
                self.assertEqual(len(calls), 3)
                name = f"{1:040x}.jpg";Image.new("RGB", (8, 8), "blue").save(source / "Covers" / name[:2] / name)
                features.run(output, args)
                self.assertEqual(len(calls), 4)
                with patch.dict(features.FEATURE_VERSIONS, {"phash": "anonymous-version-b"}):
                    features.run(output, args)
                self.assertEqual(len(calls), 7)
            with patch.object(features, "INPUT", source), patch.object(features, "load_pack", return_value=(pack, books, [1, 2, 3])), patch.object(features, "model_worker", return_value=(infer, "model-b")), contextlib.redirect_stdout(io.StringIO()):
                features.run(output, args)
                self.assertEqual(len(calls), 10)
                index = json.loads((output / "features-phash.json").read_text())
                fingerprint = index['items']['1']['fingerprint']
                path = output / "feature-cache/phash" / (fingerprint + '.json')
                damaged = json.loads(path.read_text());damaged['feature']='ffffffffffffffff'
                path.write_text(json.dumps(damaged))
                features.run(output, args)
                final = json.loads((output / "features-phash.json").read_text())
                self.assertEqual(final['items']['1']['status'], 'error')
                self.assertEqual(len(calls), 10)


if __name__ == "__main__":
    unittest.main()
