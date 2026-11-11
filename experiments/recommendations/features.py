"""Recoverable CPU-only features and descriptive, uncalibrated comparisons."""
from __future__ import annotations
import collections
import contextlib
import fcntl
import itertools
import json
import math
import os
import platform
import resource
import statistics
import time
import warnings
from core import INPUT, RULE_VERSION, atomic_json, checkpoint, cover_file, digest, file_digest, reject_symlinks, rules, validate_labels
from models import MODEL_ROOT, artifact, offline_environment

FEATURE_VERSIONS = {"phash": "phash-dct32-low8-dc-excluded-v1", "sscd": "sscd-skew320-bilinear-imagenet-v1", "minilm": "minilm-preferred-jpn-raw-title-128-mean-l2-v1"}
RSS_BUDGET = 2 * 1024**3


def rss_bytes():
    value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    return int(value if platform.system() == "Darwin" else value * 1024)


def check_resources():
    if rss_bytes() > RSS_BUDGET:
        raise MemoryError("experimental_rss_budget_exceeded")


@contextlib.contextmanager
def worker_lock(output):
    path = output / ".worker.lock"
    reject_symlinks(path)
    fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError("another_feature_worker_is_running") from None
        yield
    finally:
        os.close(fd)


def safe_image(path):
    from PIL import Image
    Image.MAX_IMAGE_PIXELS = 20_000_000
    if path.stat().st_size > 10 * 1024**2:
        raise ValueError("image_byte_limit")
    with warnings.catch_warnings():
        warnings.simplefilter("error", Image.DecompressionBombWarning)
        with Image.open(path) as image:
            if image.width <= 0 or image.height <= 0 or image.width * image.height > 20_000_000:
                raise ValueError("image_pixel_limit")
            image.load()
            return image.convert("RGB")


def phash(image):
    import numpy as np
    from PIL import Image
    pixels = np.asarray(image.convert("L").resize((32, 32), Image.Resampling.LANCZOS), dtype=np.float64)
    x = np.arange(32)
    frequencies = np.arange(8)[:, None]
    basis = np.cos(math.pi * (2 * x + 1) * frequencies / 64)
    coefficients = (basis @ pixels @ basis.T).reshape(-1)
    median = np.median(coefficients[1:])
    bits = coefficients > median
    bits[0] = False
    return f"{sum(int(value) << i for i, value in enumerate(bits)):016x}"


def phash_similarity(a, b):
    return 1.0 - (int(a, 16) ^ int(b, 16)).bit_count() / 63


def cosine(a, b):
    denominator = math.sqrt(sum(x*x for x in a) * sum(x*x for x in b))
    return max(-1.0, min(1.0, sum(x*y for x, y in zip(a, b)) / denominator)) if denominator else None


def session(path):
    import onnxruntime as ort
    options = ort.SessionOptions()
    options.intra_op_num_threads = 1
    options.inter_op_num_threads = 1
    options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
    options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_BASIC
    options.enable_mem_pattern = False
    return ort.InferenceSession(str(path), sess_options=options, providers=["CPUExecutionProvider"])


def l2(vector):
    import numpy as np
    vector = np.asarray(vector, dtype=np.float32)
    norm = float(np.linalg.norm(vector))
    if not np.isfinite(vector).all() or norm < 1e-12:
        raise ValueError("invalid_embedding")
    return (vector / norm).tolist()


def model_worker(algorithm):
    import numpy as np
    from PIL import Image
    if algorithm == "phash":
        return phash, "none"
    if algorithm == "sscd":
        conversion = json.loads((MODEL_ROOT / "sscd-conversion.json").read_text())
        path = MODEL_ROOT / "sscd_disc_mixup-skew320-opset17.onnx"
        reject_symlinks(path)
        if not conversion.get("passed") or file_digest(path) != conversion["onnx_sha256"]:
            raise ValueError("sscd_conversion_not_verified")
        model = session(path)
        def infer(image):
            # Direct square resize, no center crop; matches official skew_320 option.
            array = np.asarray(image.resize((320, 320), Image.Resampling.BILINEAR), dtype=np.float32) / 255
            array = (array - np.asarray([0.485, 0.456, 0.406], dtype=np.float32)) / np.asarray([0.229, 0.224, 0.225], dtype=np.float32)
            result = model.run(None, {"image": array.transpose(2, 0, 1)[None].copy()})[0]
            if result.shape != (1, 512):
                raise ValueError("wrong_sscd_dimensions")
            return l2(result[0])
        return infer, conversion["onnx_sha256"]
    path, sha = artifact("minilm/model.onnx")
    tokenizer_path, tokenizer_sha = artifact("minilm/tokenizer.json")
    sequence_path, sequence_sha = artifact("minilm/sentence_bert_config.json")
    pooling_path, pooling_sha = artifact("minilm/1_Pooling/config.json")
    sequence = json.loads(sequence_path.read_text())
    pooling = json.loads(pooling_path.read_text())
    if sequence.get("max_seq_length") != 128 or pooling.get("word_embedding_dimension") != 384 or not pooling.get("pooling_mode_mean_tokens") or any(pooling.get(k) for k in ("pooling_mode_cls_token", "pooling_mode_max_tokens", "pooling_mode_mean_sqrt_len_tokens")):
        raise ValueError("unsupported_minilm_official_preprocessing")
    from tokenizers import Tokenizer
    tokenizer = Tokenizer.from_file(str(tokenizer_path))
    tokenizer.enable_truncation(max_length=128)
    model = session(path)
    names = {v.name for v in model.get_inputs()}
    def infer(text):
        encoded = tokenizer.encode(text)
        values = {"input_ids": np.asarray([encoded.ids], dtype=np.int64),
                  "attention_mask": np.asarray([encoded.attention_mask], dtype=np.int64),
                  "token_type_ids": np.asarray([encoded.type_ids], dtype=np.int64)}
        result = model.run(None, {k: v for k, v in values.items() if k in names})[0]
        if result.ndim != 3 or result.shape[-1] != 384:
            raise ValueError("wrong_minilm_token_dimensions")
        mask = values["attention_mask"][..., None].astype(np.float32)
        pooled = (result * mask).sum(axis=1) / np.maximum(mask.sum(axis=1), 1e-9)
        return l2(pooled[0])
    return infer, digest([sha, tokenizer_sha, sequence_sha, pooling_sha])


def load_pack(output):
    reject_symlinks(output / "candidates.json");reject_symlinks(output / "books.json")
    pack = json.loads((output / "candidates.json").read_text())
    material = json.loads((output / "books.json").read_text())
    if material["source"]["input_fingerprint"] != pack["source"]["input_fingerprint"]:
        raise ValueError("book_input_fingerprint_mismatch")
    books = {b["id"]: b for b in material["books"]}
    ids = sorted({i for p in pack["pairs"] for i in p["book_ids"]})
    return pack, books, ids


def run(output, args, material=None, reuse_from=(), checkpoint_every=1):
    offline_environment()
    with worker_lock(output):
        started = time.monotonic()
        pack, books, ids = material if material is not None else load_pack(output)
        if args.limit:
            if args.limit < 0:
                raise ValueError("negative_feature_limit")
            ids = ids[:args.limit]
        algorithm = args.algorithm
        cache = output / "feature-cache" / algorithm
        reject_symlinks(cache)
        cache.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(cache.parent, 0o700)
        cache.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(cache, 0o700)
        startup = time.monotonic()
        infer, model_sha = model_worker(algorithm)
        startup_seconds = time.monotonic() - startup
        check_resources()
        index_path = output / ("features-" + algorithm + ".json")
        index = {"algorithm": algorithm, "version": FEATURE_VERSIONS[algorithm], "model_sha256": model_sha,
                 "pack_id": pack["pack_id"], "input_fingerprint": pack["source"]["input_fingerprint"], "items": {}}
        cached = computed = reused = 0
        timings = []
        errors = collections.Counter()
        for position, i in enumerate(ids, 1):
            checkpoint()
            b = books[i]
            try:
                if algorithm == "minilm":
                    value = (b["title_jpn"] or b["title"] or "").strip()
                    if not value:
                        raise ValueError("empty_title")
                    # Restrict the input before tokenizer allocation, then token-truncate.
                    value = value[:8192]
                    input_sha = digest(value)
                else:
                    value = cover_file(INPUT, b["cover_path"])
                    if value is None:
                        raise ValueError("missing_cover")
                    input_sha = file_digest(value)
                fingerprint = digest([FEATURE_VERSIONS[algorithm], model_sha, input_sha])
                path = cache / (fingerprint + ".json")
                reject_symlinks(path)
                if not path.exists():
                    for previous in reuse_from:
                        previous_path = previous / "feature-cache" / algorithm / path.name
                        reject_symlinks(previous_path)
                        if previous_path.exists():
                            item = json.loads(previous_path.read_text())
                            if item.get("fingerprint") != fingerprint or item.get("feature_digest") != digest(item.get("feature")):
                                raise ValueError("reused_feature_cache_corrupt")
                            atomic_json(path, item)
                            reused += 1
                            break
                if path.exists():
                    item = json.loads(path.read_text())
                    if item.get("fingerprint") != fingerprint or item.get("feature_digest") != digest(item.get("feature")):
                        raise ValueError("feature_cache_corrupt")
                    cached += 1
                else:
                    t = time.monotonic()
                    feature = infer(value if algorithm == "minilm" else safe_image(value))
                    checkpoint();check_resources()
                    timings.append(time.monotonic() - t)
                    item = {"fingerprint": fingerprint, "input_sha256": input_sha, "algorithm_version": FEATURE_VERSIONS[algorithm],
                            "model_sha256": model_sha, "feature": feature, "feature_digest": digest(feature)}
                    atomic_json(path, item)
                    computed += 1
                index["items"][str(i)] = {"status": "ok", "fingerprint": fingerprint, "feature": item["feature"]}
            except InterruptedError:
                raise
            except (OSError, ValueError, RuntimeError, Warning) as error:
                # Error text can include a private path; publish only the error type.
                category = type(error).__name__
                errors[category] += 1
                index["items"][str(i)] = {"status": "error", "category": category}
            if position % checkpoint_every == 0 or position == len(ids):
                atomic_json(index_path, index)
            if checkpoint_every > 1 and position % 250 == 0:
                print(json.dumps({"algorithm": algorithm, "processed": position, "total": len(ids)}), flush=True)
        summary = {"algorithm": algorithm, "sample_books": len(ids), "computed_unique_inputs": computed, "cache_hits": cached,
                   "reused_previous_cache": reused,
                   "errors": dict(errors), "cold_start_seconds": round(startup_seconds, 4),
                   "inference_seconds": round(sum(timings), 4), "inference_median_seconds": statistics.median(timings) if timings else None,
                   "total_seconds": round(time.monotonic() - started, 4), "peak_rss_bytes": rss_bytes(), "rss_budget_bytes": RSS_BUDGET,
                   "rss_limit_semantics": "target_checked_after_load_and_each_item_not_OS_hard_limit",
                   "cpu_threads": 1, "batch_size": 1, "execution_provider": "CPUExecutionProvider" if algorithm != "phash" else "numpy/Pillow",
                   "host_machine": platform.machine(), "host_system": platform.system(), "nas_performance_verified": False,
                   "scope": "source_copy_feature_preparation_only" if material is not None else "candidate_sample_only_not_full_library", "pack_id": pack["pack_id"]}
        atomic_json(output / ("resources-" + algorithm + ".json"), summary)
        print(json.dumps(summary))


def load_features(output, pack):
    result = {}
    for algorithm in FEATURE_VERSIONS:
        path = output / ("features-" + algorithm + ".json")
        if not path.exists():
            continue
        reject_symlinks(path)
        material = json.loads(path.read_text())
        if material["pack_id"] != pack["pack_id"] or material["version"] != FEATURE_VERSIONS[algorithm]:
            raise ValueError("feature_generation_mismatch")
        result[algorithm] = {int(i): row["feature"] for i, row in material["items"].items() if row["status"] == "ok"}
    return result


def pair_scores(a, b, books, features):
    result = {"book_ids": [a, b], "rules": rules(books[a], books[b]), "scores": {}, "conflicts": []}
    for algorithm, values in features.items():
        if a not in values or b not in values:
            continue
        result["scores"][algorithm] = phash_similarity(values[a], values[b]) if algorithm == "phash" else cosine(values[a], values[b])
    if result["rules"]["volume_conflict"]:
        result["conflicts"].append("version_cross_volume_or_chapter_block")
    if books[a]["collection_id"] is not None and books[b]["collection_id"] is not None and books[a]["collection_id"] != books[b]["collection_id"]:
        result["conflicts"].append("existing_different_series_requires_manual_resolution")
    if books[a]["version_group_id"] is not None and books[b]["version_group_id"] is not None and books[a]["version_group_id"] != books[b]["version_group_id"]:
        result["conflicts"].append("existing_different_version_groups_requires_manual_resolution")
    return result


def score(output, args):
    offline_environment()
    with worker_lock(output):
        pack, books, ids = load_pack(output)
        features = load_features(output, pack)
        rows = []
        for pair in pack["pairs"]:
            row = pair_scores(*pair["book_ids"], books, features)
            row.update({"pair_id": pair["pair_id"], "task": pair["task"], "split": pair["split"]})
            rows.append(row)
        pool = {}
        neighbors = collections.defaultdict(list)
        for a, b in itertools.combinations(ids, 2):
            checkpoint()
            row = pair_scores(a, b, books, features)
            pool[(a, b)] = row
            for task, algorithms in (("version", ("rule", "phash", "sscd")), ("series", ("rule", "minilm"))):
                if task == "version" and row["rules"]["version_blocked"]:
                    continue
                if task == "series" and books[a]["version_group_id"] is not None and books[a]["version_group_id"] == books[b]["version_group_id"]:
                    continue
                for algorithm in algorithms:
                    value = row["rules"][task + "_score"] if algorithm == "rule" else row["scores"].get(algorithm)
                    if value is None:
                        continue
                    neighbors[(task, algorithm, a)].append((value, b))
                    neighbors[(task, algorithm, b)].append((value, a))
        union = collections.defaultdict(set)
        route_edges = collections.Counter()
        for (task, algorithm, a), values in neighbors.items():
            for value, b in sorted(values, key=lambda x: (-x[0], x[1]))[:5]:
                union[(task, *sorted((a, b)))].add(algorithm)
        suggestions = []
        for (task, a, b), routes in sorted(union.items()):
            row = pool[(a, b)]
            suggestions.append({**row, "task": task, "routes": sorted(routes), "status": "unthresholded_candidate_for_human_review"})
            route_edges.update(task + ":" + route for route in routes)
        result = {"schema": "pair-score-v1", "pack_id": pack["pack_id"], "rule_version": RULE_VERSION,
                  "feature_versions": FEATURE_VERSIONS, "score_semantics": "matching_signals_not_probability",
                  "labeled_sample_only_no_full_library_recall": True, "rows": rows, "union_candidates": suggestions,
                  "automatic_group_expansion": False, "top_k_per_route": 5}
        atomic_json(output / "scores.json", result)
        distributions = {}
        for task in ("version", "series"):
            for algorithm in ("rule", *FEATURE_VERSIONS):
                values = [r["rules"][task + "_score"] if algorithm == "rule" else r["scores"].get(algorithm) for r in rows if r["task"] == task]
                values = sorted(v for v in values if v is not None)
                if values:
                    distributions[task + ":" + algorithm] = {"count": len(values), "min": min(values), "median": statistics.median(values), "max": max(values)}
        summary = {"pairs": len(rows), "unthresholded_union_pairs": len(suggestions), "route_counts": dict(route_edges),
                   "distributions": distributions, "labels_used": 0, "thresholds_calibrated": False,
                   "version_cross_volume_blocks": sum(r["rules"]["version_blocked"] for r in rows if r["task"] == "version")}
        atomic_json(output / "descriptive-summary.json", summary)
        print(json.dumps(summary))


def evaluate(output, args):
    # Reading user exports is explicit; there is no auto-poll of partially written files.
    path = output / "labels-reviewed.json"
    reject_symlinks(path)
    if not path.exists():
        raise ValueError("human_labels_required_run_validate_labels_after_user_confirms_export")
    pack, books, ids = load_pack(output)
    labels = validate_labels(pack, json.loads(path.read_text()))
    reject_symlinks(output / "scores.json")
    rows = json.loads((output / "scores.json").read_text())
    if rows["pack_id"] != pack["pack_id"] or rows["rule_version"] != RULE_VERSION:
        raise ValueError("stale_pair_scores")
    labeled = {r["pair_id"]: r for r in labels["annotations"] if r["label"] in ("same", "different")}
    if not labeled:
        raise ValueError("no_decisive_human_labels")
    distribution = collections.defaultdict(list)
    for row in rows["rows"]:
        if row["pair_id"] not in labeled:
            continue
        label = labeled[row["pair_id"]]["label"]
        distribution[(row["task"], label, "rule")].append(row["rules"][row["task"] + "_score"])
        for algorithm, value in row["scores"].items():
            if value is not None:
                distribution[(row["task"], label, algorithm)].append(value)
    result = {"status": "human_labeled_descriptive_pilot", "decisive_labels": len(labeled),
              "uncertain_labels": sum(r["label"] == "uncertain" for r in labels["annotations"]),
              "labeled_distributions": {":".join(key): {"count": len(values), "median": statistics.median(values), "min": min(values), "max": max(values)} for key, values in distribution.items()},
              "final_thresholds": None, "accuracy_claim": None,
              "limitations": ["pilot_pack_is_exploratory", "formal_70_30_family_audit_and_sample_required", "no_full_library_recall_or_NAS_performance_claim"]}
    annotations = {r["pair_id"]: r for r in labels["annotations"]}
    result["label_counts"] = dict(collections.Counter(p["task"] + ":" + str(annotations.get(p["pair_id"], {}).get("label") or "unlabeled") for p in pack["pairs"]))
    result["stratum_labels"] = dict(collections.Counter(p["task"] + ":" + p["stratum"] + ":" + str(annotations.get(p["pair_id"], {}).get("label")) for p in pack["pairs"]))
    different_strata = collections.Counter(p["stratum"] for p in pack["pairs"] if annotations.get(p["pair_id"], {}).get("label") == "different")
    ordinary_only = bool(different_strata) and set(different_strata) == {"ordinary_negative_candidate"}
    result["sampling_bias"] = {"different_labels_by_stratum": dict(different_strata),
                               "all_decisive_different_from_ordinary_stratum": ordinary_only,
                               "hard_candidate_rule_priority_favors_rule_baseline": True,
                               "hard_negative_reliability_established": False,
                               "algorithm_superiority_claim_allowed": False}
    result["score_separation"] = {}
    result["known_positive_sample_top5"] = {}
    diagnostics = []
    union = {(r["task"], *r["book_ids"]): set(r["routes"]) for r in rows["union_candidates"]}
    for task, algorithms in (("version", ("rule", "phash", "sscd")), ("series", ("rule", "minilm"))):
        positives = [r for r in rows["rows"] if r["task"] == task and labeled.get(r["pair_id"], {}).get("label") == "same"]
        negatives = [r for r in rows["rows"] if r["task"] == task and labeled.get(r["pair_id"], {}).get("label") == "different"]
        for algorithm in algorithms:
            def value(row):
                return row["rules"][task + "_score"] if algorithm == "rule" else row["scores"].get(algorithm)
            pos = [r for r in positives if value(r) is not None]
            neg = [r for r in negatives if value(r) is not None]
            hits = [r for r in pos if algorithm in union.get((task, *sorted(r["book_ids"])), set())]
            result["known_positive_sample_top5"][task + ":" + algorithm] = {"found": len(hits), "labeled_same": len(positives), "missed": len(positives)-len(hits),
                "feature_available": len(pos), "feature_missing": len(positives)-len(pos), "pool_books": len(ids),
                "scope": "known_annotated_pairs_within_pilot_pool_not_library_recall"}
            for row in pos:
                if algorithm not in union.get((task, *sorted(row["book_ids"])), set()):
                    diagnostics.append({"pair_id": row["pair_id"], "algorithm": algorithm, "kind": "human_same_outside_route_top5", "score": value(row)})
            if pos and neg:
                minimum = min(value(r) for r in pos);maximum = max(value(r) for r in neg)
                result["score_separation"][task + ":" + algorithm] = {"same_min": minimum, "different_max": maximum, "observed_overlap": minimum <= maximum, "same_count": len(pos), "different_count": len(neg), "is_threshold_calibration": False}
                for row in pos:
                    if value(row) <= maximum:
                        diagnostics.append({"pair_id": row["pair_id"], "algorithm": algorithm, "kind": "human_same_at_or_below_observed_different_max", "score": value(row)})
                for row in neg:
                    if value(row) >= minimum:
                        diagnostics.append({"pair_id": row["pair_id"], "algorithm": algorithm, "kind": "human_different_at_or_above_observed_same_min", "score": value(row)})
        union_hits = sum(bool(union.get((task, *sorted(r["book_ids"])), set())) for r in positives)
        result["known_positive_sample_top5"][task + ":union"] = {"found": union_hits, "labeled_same": len(positives), "missed": len(positives)-union_hits, "pool_books": len(ids), "scope": "known_annotated_pairs_only_not_library_recall"}
        for row in positives:
            if task == "version" and row["rules"]["version_blocked"]:
                diagnostics.append({"pair_id": row["pair_id"], "algorithm": "rule", "kind": "explicit_ordinal_guard_conflicts_with_human_same"})
    atomic_json(output / "diagnostics.json", {"pack_id": pack["pack_id"], "not_final_misclassification": True, "items": diagnostics})
    diagnostic_ids = {r["pair_id"] for r in diagnostics}
    if diagnostic_ids:
        from review import render_diagnostics
        render_diagnostics(pack, books, INPUT, annotations, rows["rows"], diagnostics, output / "review-outliers.html")
    result["diagnostic_pairs"] = len(diagnostic_ids)
    atomic_json(output / "human-labeled-pilot.json", result)
    print(json.dumps(result))
