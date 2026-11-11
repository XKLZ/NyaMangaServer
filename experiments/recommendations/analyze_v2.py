"""Calibration-sealed, cached-only relationship analysis; never opens source DB/images.

Run calibrate once, then evaluate once after the coordinator releases heldout.json.
All individual records and input identifiers stay in the private analysis directory.
"""
from __future__ import annotations
import argparse
import collections
import hashlib
import html
import itertools
import json
import math
import os
from pathlib import Path
import platform
import re
import resource
import sys
import time
import unicodedata

# Set before importing NumPy. No model runtime, tokenizer, image or network imports.
for _name in ("OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS", "VECLIB_MAXIMUM_THREADS", "NUMEXPR_NUM_THREADS"):
    os.environ[_name] = "1"
import numpy as np
from core import atomic_json, digest, file_digest, reject_symlinks, rules as legacy_rules
from features import FEATURE_VERSIONS

VERSION = "relationship-analysis-v2.1"
ROOT = Path(__file__).resolve().parents[2]
DIRECTORY = ROOT / ".runtime/tests/runs/recommendations/run-v2/analysis"
LABELS = {"same_work", "same_series", "unrelated", "uncertain"}
TASKS = {"version": "same_work", "series": "same_series"}
BASE_THRESHOLDS = {"rule": {"version": .9, "series": .9}, "phash": {"version": .9, "series": .9},
                   "sscd": {"version": .7, "series": .7}, "minilm": {"version": .9, "series": .9}}
BASELINE_NOTE = "fixed_conventional_signal_thresholds_not_previous_label_calibration; images_for_series_are_diagnostics"
CODE_FILES = ["analyze_v2.py", "test_analysis_v2.py", "core.py", "features.py", "models.py", "model-artifacts.lock.json",
              "requirements-runtime.txt", "requirements-lock-macos-arm64.txt"]
# Finite grid declared before calibration. No new model, training, or stochastic search.
GRID = {"work_threshold": [.82, .86, .90], "series_threshold": [.72, .78, .84],
        "title_weight": [.78, .86], "image_weight": [.04, .08]}
PRIMARY_POLICY = "exclude label=uncertain or collection_or_partial_overlap=true; preserve and report every row"
BRACKETS = re.compile(r"\[[^\[\]]*\]|\([^()]*\)|【[^【】]*】|《[^《》]*》|≪[^≪≫]*≫")
PREFIX = re.compile(r"^\s*\[([^\[\]]*(?:\([^()]*\)[^\[\]]*)?)\]\s*")
EVENT = re.compile(r"(?:c\d{1,3}|comic1[☆*]?\d{1,3}|comitia\d{1,3}|サンクリ\d+(?:\s*(?:summer|winter|spring|autumn))?|(?:秋季)?例大祭\d+|紅楼夢\d+|cレヴォ\d+)", re.I)
DECORATION = re.compile(r"(?:english|chinese|japanese|translated|digital|full[ -]?colou?r|uncensored|カラー(?:化)?|無修正|修正版|書換え版|完全版|英訳|中国(?:翻訳|語)|英語翻訳|日本語|中文|全彩|漢化|汉化|翻訳|dl版|dl去碼|[\w\sー・]+(?:個人)?(?:漢化|汉化)(?:去碼)?|[\w\sー・]+(?:漢化|汉化)组)", re.I)
DATE = re.compile(r"(?:\d{4}年\d{1,2}月(?:\d{1,2}日)?|\d{4}[.\-/]\d{1,2}(?:[.\-/]\d{1,2})?)")
NUMBER = r"(?:\d{1,4}(?:\.\d{1,2})?|[一二三四五六七八九十]{1,3})"
ORDINAL = re.compile(r"\b(?P<latin>vol(?:ume)?\.?|chapter|ch\.?)\s*(?P<n>\d{1,4}(?:\.\d{1,2})?)\b|(?:第\s*)?(?P<cn>" + NUMBER + r")\s*(?P<unit>巻|卷|話|话|章)|(?P<section>上|中|下|前|後|后)(?:編|篇|巻|卷)", re.I)
SUFFIX_NUMBER = re.compile(r"(?P<num>\d{1,3}(?:\.\d{1,2})?)(?:\s*)$")


def norm(value):
    return unicodedata.normalize("NFKC", html.unescape(value or "")).casefold().strip()


def key(value):
    return "".join(c for c in norm(value) if c.isalnum())


def grams(value):
    return set(value[i:i + 2] for i in range(max(0, len(value) - 1)))


def jac(a, b):
    return len(a & b) / len(a | b) if a and b else 0.


def tags(book, namespace):
    return {key(v) for ns, v in book.get("tags", []) if ns == namespace and key(v)}


def creator_sets(book):
    return {ns: tags(book, ns) for ns in ("artist", "group")}


def build_aliases(books):
    """Only repeated [Group (Artist)] signatures with unanimous creator-tag support become aliases.

    Unknown unstructured and single-use brackets stay intact. Tag intersections prevent
    a common anthology prefix with changing artists from becoming a creator alias.
    """
    evidence = collections.defaultdict(list)
    for book in books.values():
        match = PREFIX.match(norm(book.get("title_jpn") or book.get("title")))
        if match and re.fullmatch(r"[^()]+\([^()]+\)\s*", match[1]):
            evidence[key(match[1])].append(creator_sets(book))
    aliases = {}
    for signature, rows in evidence.items():
        supported = {}
        if len(rows) >= 2:
            for ns in ("artist", "group"):
                common = set.intersection(*(row[ns] for row in rows))
                if common:
                    supported[ns] = sorted(common)
        if supported:
            aliases[signature] = supported
    return aliases


def number(value):
    if re.fullmatch(r"\d+(?:\.\d+)?", value):
        return str(float(value)).removesuffix(".0")
    digits = {c: i for i, c in enumerate("零一二三四五六七八九")}
    if value == "十":
        return "10"
    if "十" in value and value.count("十") == 1:
        left, right = value.split("十")
        if len(left) <= 1 and len(right) <= 1:
            return str((digits.get(left, 1) if left else 1) * 10 + (digits.get(right, 0) if right else 0))
    return str(digits[value]) if value in digits else None


def structure(book, aliases):
    text = norm(book.get("title_jpn") or book.get("title"))
    removed = []
    # Event and decoration brackets are explicitly recognized, not arbitrary brackets.
    def clean_bracket(match):
        inner = match[0][1:-1].strip()
        if EVENT.fullmatch(inner) or DECORATION.fullmatch(inner) or DATE.fullmatch(inner):
            removed.append("recognized_modifier")
            return " "
        return match[0]
    text = BRACKETS.sub(clean_bracket, text).strip()
    creators = creator_sets(book)
    match = PREFIX.match(text)
    if match:
        signature = key(match[1])
        direct = any(signature in creators[ns] for ns in creators)
        alias = aliases.get(signature, {})
        supported = any(set(values) & creators[ns] for ns, values in alias.items())
        if direct or supported:
            text = text[match.end():].strip()
            removed.append("creator_prefix_tag_supported")
    # Protect unknown bracket contents from ordinal parsing (e.g. magazine volume).
    spans = []
    protected = re.compile(r"\[[^\]]*\]|\([^)]*\)|【[^】]*】")
    for match in protected.finditer(text):
        spans.append((match.start(), match.end()))
    ordinals, pieces, previous = [], [], 0
    for match in ORDINAL.finditer(text):
        if any(start <= match.start() < end for start, end in spans):
            continue
        if match["section"]:
            kind, value = "section", {"上": "1", "前": "1", "中": "2", "下": "3", "後": "3", "后": "3"}[match["section"]]
        else:
            value = number(match["n"] or match["cn"])
            kind = "volume" if (match["latin"] or "").startswith("vol") or match["unit"] in ("巻", "卷") else "chapter"
        if value is None:
            continue
        ordinals.append([kind, value])
        pieces.extend([text[previous:match.start()], " "])
        previous = match.end()
    pieces.append(text[previous:])
    stem_text = "".join(pieces)
    # Bare numbers remain in exact identity, and are only a tentative series stem.
    # Only a main-title suffix / number before a subtitle is considered. Intrinsic
    # numbers such as 007 and 3x3 are retained and cannot hard-block a work action.
    main = re.split(r"[\[(【]", stem_text, maxsplit=1)[0].strip()
    tentative = None
    m = SUFFIX_NUMBER.search(main)
    if m and m.start() > 2 and not (m["num"].startswith("0") and len(m["num"]) > 1):
        tentative = m["num"]
        tentative_stem = key(main[:m.start()])
    else:
        tentative_stem = key(main)
    # A finite bare Roman / parenthesized-number suffix is a tentative position.
    # The original core and unknown bracket contents remain available unchanged.
    roman = re.search(r"(?<![a-z])(?P<roman>viii|vii|iii|vi|iv|ii|ix|v|x|i)$", main)
    parenthesized = re.match(r"^\s*(.+?)\s*\((\d{1,3}(?:\.\d{1,2})?)\)(?:\s*$|\s*[\[(【])", stem_text)
    if tentative is None and roman and roman.start() >= 4:
        tentative = str({"i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8, "ix": 9, "x": 10}[roman["roman"]])
        tentative_stem = key(main[:roman.start()])
    elif tentative is None and parenthesized:
        tentative = parenthesized[2]
        tentative_stem = key(parenthesized[1])
    # Prefix before a nonterminal number can retrieve a numbered subtitle series,
    # but is ambiguous and never alone sufficient to classify a relationship.
    inline = re.search(r"(?<!\d)([1-9]\d{0,2}(?:\.\d{1,2})?)(?!\d)", main)
    prefix = key(main[:inline.start()]) if inline and inline.start() >= 4 else ""
    return {"core": key(text), "stem": key(stem_text), "grams": grams(key(text)),
            "stem_grams": grams(key(stem_text)), "main": key(main), "tentative_stem": tentative_stem,
            "tentative_number": tentative, "number_prefix": prefix, "ordinals": ordinals,
            "removed": removed, "empty": not bool(key(text)),
            "compilation_hint": bool(re.search(r"総集編|compilation|合集|\d+\s*(?:&|[-～])\s*\d+", text))}


def creator_relation(a, b):
    states = {}
    for ns in ("artist", "group"):
        aa, bb = tags(a, ns), tags(b, ns)
        states[ns] = "missing" if not aa or not bb else ("shared" if aa & bb else "conflict")
    # A shared artist may use different groups; group differences alone are caution.
    return {"states": states, "shared": "shared" in states.values(),
            "conflict": states["artist"] == "conflict" and states["group"] != "shared",
            "all_missing": all(v == "missing" for v in states.values())}


def features_for_pair(a, b, features):
    result = {}
    for algorithm, material in features.items():
        aa, bb = material.get(a), material.get(b)
        if aa is None or bb is None:
            result[algorithm] = None
        elif algorithm == "phash":
            result[algorithm] = 1 - (aa ^ bb).bit_count() / 63
        else:
            result[algorithm] = max(-1., min(1., float(np.dot(aa, bb))))
    return result


def signals(a, b, structures, features, books):
    sa, sb = structures[a], structures[b]
    creator = creator_relation(books[a], books[b])
    title = jac(sa["grams"], sb["grams"])
    stem = jac(sa["stem_grams"], sb["stem_grams"])
    same_core = bool(sa["core"] and sa["core"] == sb["core"])
    same_stem = bool(sa["stem"] and sa["stem"] == sb["stem"])
    explicit_difference = bool(sa["ordinals"] and sb["ordinals"] and sa["ordinals"] != sb["ordinals"])
    explicit_same = bool(sa["ordinals"] and sa["ordinals"] == sb["ordinals"])
    tentative_same = bool(len(sa["tentative_stem"]) >= 4 and sa["tentative_stem"] == sb["tentative_stem"] and (sa["tentative_number"] is not None or sb["tentative_number"] is not None))
    tentative_difference = tentative_same and sa["tentative_number"] != sb["tentative_number"]
    prefixes = [sa["number_prefix"], sb["number_prefix"]]
    prefix_same = bool(len(prefixes[0]) >= 5 and prefixes[0] == prefixes[1])
    series_title = max(stem, .98 if tentative_same else 0, .88 if prefix_same else 0)
    page_a, page_b = books[a].get("pages") or 0, books[b].get("pages") or 0
    page_ratio = min(page_a, page_b) / max(page_a, page_b) if page_a and page_b else None
    scores = features_for_pair(a, b, features)
    return {"title": title, "series_title": series_title, "same_core": same_core, "same_stem": same_stem,
            "explicit_difference": explicit_difference, "explicit_same": explicit_same,
            "tentative_difference": tentative_difference, "prefix_same": prefix_same,
            "creator": creator, "pages": page_ratio, "scores": scores, "same_main": bool(sa["main"] and sa["main"] == sb["main"]),
            "empty_title": sa["empty"] or sb["empty"], "ordinal_ambiguity": bool(sa["ordinals"]) != bool(sb["ordinals"]),
            "ordinal_kind_ambiguity": bool(sa["ordinals"] and sb["ordinals"] and {p[0] for p in sa["ordinals"]} != {p[0] for p in sb["ordinals"]}),
            "possible_partial_overlap": sa["compilation_hint"] != sb["compilation_hint"] and page_ratio is not None and page_ratio < .75}


def scored(signal, params):
    image_values = [v for name, v in signal["scores"].items() if name in ("phash", "sscd") and v is not None]
    image = max(0., min(1., max(image_values))) if image_values else 0.
    semantic = max(0., signal["scores"].get("minilm") or 0.)
    creator = float(signal["creator"]["shared"])
    tw, iw = params["title_weight"], params["image_weight"]
    title_support = signal["title"]
    if signal.get("same_main") and not signal["tentative_difference"] and (signal["scores"].get("sscd") or 0) >= .85 and semantic >= .9:
        title_support = max(title_support, .98)
    work = tw * title_support + iw * image + .04 * creator + (1 - tw - iw - .04) * semantic
    if signal["tentative_difference"]:
        work -= .12  # Ambiguous position is a caution penalty, never a hard conflict.
    series = .76 * signal["series_title"] + .12 * creator + .12 * semantic
    return {"version": round(work, 8), "series": round(series, 8)}


def predict(signal, params):
    scores = scored(signal, params)
    reasons = []
    if signal["empty_title"]:
        reasons.append("missing_title")
    if signal["creator"]["conflict"]:
        reasons.append("explicit_creator_conflict")
    if signal.get("ordinal_kind_ambiguity"):
        reasons.append("incompatible_ordinal_types_need_review")
    if signal.get("possible_partial_overlap"):
        reasons.append("possible_compilation_or_partial_overlap")
    if reasons:
        return {"action": "defer", "scores": scores, "reasons": reasons}
    # Different explicit positions or tentative bare numbers mean work suggestions
    # need review. They can support a series action with strong title evidence.
    ordinal_difference = signal["explicit_difference"] or signal["tentative_difference"]
    image_auxiliary = (signal["scores"].get("sscd") or 0) >= .85 and (signal["scores"].get("minilm") or 0) >= .90 and signal["creator"]["shared"] and (signal["pages"] or 0) >= .8 and signal["title"] >= .3
    work_candidate = (scores["version"] >= params["work_threshold"] and (signal["title"] >= .66 or signal.get("same_main"))) or image_auxiliary
    work_candidate = work_candidate and not signal["explicit_difference"] and not signal["ordinal_ambiguity"]
    distinct = ordinal_difference or (not signal["same_core"] and not signal["same_stem"] and not signal.get("same_main") and signal["title"] < .93)
    series_candidate = scores["series"] >= params["series_threshold"] and signal["series_title"] >= .62 and distinct
    # Different versions must not become two logical series entries.
    if work_candidate and series_candidate:
        return {"action": "defer", "scores": scores, "reasons": ["competing_work_and_series_evidence"]}
    if work_candidate:
        return {"action": "same_work", "scores": scores, "reasons": ["high_image_and_semantic_with_compatible_creator_and_pages" if image_auxiliary and signal["title"] < .66 else "strong_title_with_auxiliary_support"]}
    if series_candidate:
        return {"action": "same_series", "scores": scores, "reasons": ["related_title_distinct_logical_entry_evidence"]}
    if signal["title"] < .25 and signal["series_title"] < .35 and (signal["scores"].get("minilm") or 0) < .5 and not image_close(signal):
        return {"action": "unrelated", "scores": scores, "reasons": ["multiple_low_similarity_signals"]}
    return {"action": "defer", "scores": scores, "reasons": ["insufficient_or_ambiguous_evidence"]}


def image_close(signal):
    return (signal["scores"].get("sscd") or 0) >= .85 or (signal["scores"].get("phash") or 0) >= .95


def primary(row):
    return row["label"] != "uncertain" and not row["collection_or_partial_overlap"]


def wilson(success, total):
    if not total:
        return None
    z = 1.959963984540054
    p = success / total
    center = (p + z*z / (2*total)) / (1 + z*z/total)
    half = z * math.sqrt(p*(1-p)/total + z*z/(4*total*total)) / (1 + z*z/total)
    return [max(0., center-half), min(1., center+half)]


def metrics(rows, decisions, task, conditional=False):
    selected = [r for r in rows if primary(r) and (not conditional or r["label"] != "same_work")]
    tp = fp = fn = tn = deferred = 0
    for row in selected:
        positive = row["label"] == TASKS[task]
        action = decisions[row["pair_id"]]
        predicted = action == TASKS[task]
        tp += positive and predicted
        fp += not positive and predicted
        fn += positive and not predicted  # Includes deferred positives: conservative recall.
        tn += not positive and not predicted
        deferred += action == "defer"
    return {"samples": len(selected), "positives": tp+fn, "negatives": fp+tn, "tp": tp, "fp": fp,
            "fn": fn, "tn_including_defer": tn, "precision": tp/(tp+fp) if tp+fp else None,
            "recall": tp/(tp+fn) if tp+fn else None, "precision_wilson95": wilson(tp, tp+fp),
            "deferred": deferred, "defer_fraction": deferred/len(selected) if selected else None,
            "conditional_excludes_same_work": conditional, "fn_includes_deferred_positives": True,
            "automatic_coverage_fraction": (len(selected)-deferred)/len(selected) if selected else None,
            "positive_action_predictions": tp+fp}


def baseline_decisions(rows, pairs, books, name, task, threshold=None):
    threshold = BASE_THRESHOLDS[name][task] if threshold is None else threshold
    out = {}
    for row in rows:
        a, b = row["book_ids"]
        if name == "rule":
            legacy = legacy_rules(books[a], books[b])
            value = legacy[task+"_score"]
            block = task == "version" and legacy["version_blocked"]
        else:
            value = pairs[row["pair_id"]]["scores"].get(name)
            block = False
        out[row["pair_id"]] = "defer" if value is None or block else (TASKS[task] if value >= threshold else "unrelated")
    return out


def calibrate_single_signals(rows, pairs, books):
    grids = {"rule": [.5, .6, .7, .8, .9, .95], "phash": [.6, .7, .8, .85, .9, .95],
             "sscd": [.4, .5, .6, .7, .8, .9], "minilm": [.6, .7, .8, .85, .9, .95]}
    result = {}
    for name, candidates in grids.items():
        result[name] = {}
        for task in TASKS:
            trials = []
            for threshold in candidates:
                measured = metrics(rows, baseline_decisions(rows, pairs, books, name, task, threshold), task)
                trials.append((threshold, measured))
            feasible = [(th, m) for th, m in trials if m["precision"] is not None and m["precision"] >= .95 and m["positive_action_predictions"] >= 10]
            winner = max(feasible, key=lambda trial: (trial[1]["recall"], -trial[1]["fp"], trial[0])) if feasible else next(trial for trial in trials if trial[0] == BASE_THRESHOLDS[name][task])
            result[name][task] = {"threshold": winner[0], "calibration_target_met": bool(feasible), "candidate_thresholds": candidates,
                                  "fallback": "fixed_baseline_threshold_if_no_feasible_configuration", "calibration_metrics": winner[1]}
    return result


def grid_search(rows, pairs):
    trials = []
    for values in itertools.product(*(GRID[k] for k in GRID)):
        params = dict(zip(GRID, values))
        decisions = {r["pair_id"]: predict(pairs[r["pair_id"]], params)["action"] for r in rows}
        measured = {task: metrics(rows, decisions, task) for task in TASKS}
        # Precision is the first constraint; maximize conservative recall only among
        # configurations meeting 95% point precision in both actions. Not a guarantee.
        meets = all(m["precision"] is not None and m["precision"] >= .95 and m["tp"] + m["fp"] >= 10 for m in measured.values())
        total_fp = sum(m["fp"] for m in measured.values())
        total_tp = sum(m["tp"] for m in measured.values())
        rank = (meets, sum(m["recall"] or 0 for m in measured.values()) if meets else 0, -total_fp, total_tp, params["work_threshold"] + params["series_threshold"], params["title_weight"], -params["image_weight"])
        trials.append({"parameters": params, "metrics": measured, "rank": rank})
    feasible = [trial for trial in trials if trial["rank"][0]]
    fallback = {"work_threshold": .90, "series_threshold": .84, "title_weight": .86, "image_weight": .04}
    winner = max(feasible, key=lambda trial: trial["rank"]) if feasible else next(trial for trial in trials if trial["parameters"] == fallback)
    return winner["parameters"], [{k: v for k, v in trial.items() if k != "rank"} for trial in trials]


def validate_rows(material, books, expected_schema):
    if material.get("schema") != expected_schema or not isinstance(material.get("rows"), list):
        raise ValueError("invalid_analysis_input_schema")
    ids, pair_keys = set(), set()
    for row in material["rows"]:
        if not isinstance(row, dict) or set(row) != {"pair_id", "book_ids", "family_ids", "label", "reason", "collection_or_partial_overlap"}:
            raise ValueError("invalid_analysis_row_fields")
        pair = row["book_ids"]
        if not isinstance(row["pair_id"], str) or row["pair_id"] in ids or not isinstance(pair, list) or len(pair) != 2 or any(type(i) is not int or i not in books for i in pair) or pair[0] == pair[1]:
            raise ValueError("invalid_or_duplicate_pair")
        pair_key = tuple(sorted(pair))
        if pair_key in pair_keys:
            raise ValueError("duplicate_book_pair")
        if row["label"] not in LABELS or type(row["collection_or_partial_overlap"]) is not bool or not isinstance(row["reason"], str) or len(row["reason"]) > 4000:
            raise ValueError("invalid_annotation")
        if not isinstance(row["family_ids"], list) or not row["family_ids"] or any(not isinstance(f, str) for f in row["family_ids"]):
            raise ValueError("invalid_families")
        ids.add(row["pair_id"]); pair_keys.add(pair_key)
    return material["rows"]


def read_json(path):
    reject_symlinks(path)
    return json.loads(path.read_text())


def load_inputs(directory):
    book_material = read_json(directory / "analysis-books.json")
    allowed = {"id", "title", "title_jpn", "pages", "tags", "language_code", "is_translated", "is_full_color", "censorship", "has_extraneous_ads"}
    if any(set(b) - allowed for b in book_material["books"]):
        raise ValueError("unapproved_book_fields")
    books = {b["id"]: b for b in book_material["books"]}
    calibration = read_json(directory / "calibration.json")
    validate_rows(calibration, books, "recommendation-calibration-v2")
    audit = read_json(directory / "input-audit.json")
    if audit["calibration_sha256"] != file_digest(directory / "calibration.json") or audit["analysis_books_sha256"] != file_digest(directory / "analysis-books.json") or audit["pack_id"] != calibration["pack_id"]:
        raise ValueError("input_audit_mismatch")
    features = {}
    feature_metadata = {}
    for algorithm, version in FEATURE_VERSIONS.items():
        path = directory.parent / ("features-" + algorithm + ".json")
        material = read_json(path)
        if material["pack_id"] != calibration["feature_input_id"] or material["input_fingerprint"] != book_material["source"]["input_fingerprint"] or material["version"] != version:
            raise ValueError("feature_input_or_version_mismatch")
        feature_metadata[algorithm] = {k: v for k, v in material.items() if k != "items"}
        values = {}
        for raw_id, item in material["items"].items():
            i = int(raw_id)
            if i not in books or item.get("status") != "ok":
                continue
            value = item.get("feature")
            if algorithm == "phash":
                if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{16}", value):
                    continue
                values[i] = int(value, 16)
            else:
                v = np.asarray(value, dtype=np.float32)
                if v.shape != ((512,) if algorithm == "sscd" else (384,)) or not np.isfinite(v).all():
                    continue
                magnitude = float(np.linalg.norm(v))
                if not .999 <= magnitude <= 1.001:
                    continue
                values[i] = v / magnitude
        features[algorithm] = values
    return books, calibration, audit, features, feature_metadata


def retrieval(rows, books, structures, features, parameters):
    """Rank positive endpoints against the entire whitelist, never against sampled pairs.

    Pair is covered if either endpoint retrieves its annotated positive counterpart.
    Ties sort by integer ID. Ranking is candidate generation, not an automatic action.
    """
    positives = {task: [r for r in rows if primary(r) and r["label"] == label] for task, label in TASKS.items()}
    queries = sorted({i for rr in positives.values() for r in rr for i in r["book_ids"]})
    rank_maps = {}
    all_ids = sorted(books)
    for query in queries:
        rankings = collections.defaultdict(list)
        for other in all_ids:
            if query == other:
                continue
            sig = signals(query, other, structures, features, books)
            improved = scored(sig, parameters)
            old = legacy_rules(books[query], books[other])
            for task in TASKS:
                rankings[(task, "rule")].append((old[task+"_score"], other))
                rankings[(task, "improved")].append((improved[task], other))
                for route, value in sig["scores"].items():
                    if value is not None:
                        rankings[(task, route)].append((value, other))
        rank_maps[query] = {route: {i: rank for rank, (_, i) in enumerate(sorted(values, key=lambda item: (-item[0], item[1]))[:20], 1)} for route, values in rankings.items()}
    out = {}
    for task, rr in positives.items():
        for route in ("rule", "phash", "sscd", "minilm", "improved", "union"):
            for k in (5, 10, 20):
                hits = available = 0
                for row in rr:
                    a, b = row["book_ids"]
                    routes = ("rule", "phash", "sscd", "minilm", "improved") if route == "union" else (route,)
                    found = any(rank_maps[a].get((task, rt), {}).get(b, 21) <= k or rank_maps[b].get((task, rt), {}).get(a, 21) <= k for rt in routes)
                    hits += found
                    available += route in ("rule", "improved", "union") or (a in features[route] and b in features[route])
                out[f"{task}:{route}:top{k}"] = {"found": hits, "labeled_positive_pairs": len(rr), "coverage": hits/len(rr) if rr else None,
                    "missing_pair_features": len(rr)-available, "pool_books": len(books), "pair_covered_if_either_endpoint_in_top_k": True,
                    "scope": "annotated_positive_pair_coverage_not_full_library_recall", "rankings_do_not_apply_action_guards": True,
                    "union_semantics": "union_of_each_route_top_K_not_a_total_top_K" if route == "union" else None,
                    "maximum_emitted_candidates_per_query": k * (5 if route == "union" else 1),
                    "union_route_count": 5 if route == "union" else 1}
    return out


def label_consistency(rows):
    graph = collections.defaultdict(dict)
    for row in rows:
        if primary(row):
            a, b = row["book_ids"]
            graph[a][b] = row; graph[b][a] = row
    issues = []
    for a in sorted(graph):
        for b, c in itertools.combinations(sorted(i for i in graph[a] if i > a), 2):
            if c not in graph[b]:
                continue
            triangle = [graph[a][b], graph[a][c], graph[b][c]]
            labels = [r["label"] for r in triangle]
            count_work, count_series = labels.count("same_work"), labels.count("same_series")
            inconsistent = count_work == 2 and count_series != 0 or count_work == 2 and "unrelated" in labels or count_series >= 2 and "unrelated" in labels or count_work == 1 and count_series == 1 and "unrelated" in labels
            if inconsistent:
                issues.append({"pair_ids": [r["pair_id"] for r in triangle], "labels": labels, "kind": "explicit_labeled_triangle_needs_human_review"})
    return issues


def report(rows, pairs, decisions, parameters, books, structures, features, calibrated_baselines):
    baseline = {}
    for name in BASE_THRESHOLDS:
        baseline[name] = {task: metrics(rows, baseline_decisions(rows, pairs, books, name, task), task) for task in TASKS}
    calibrated_measured = {name: {task: metrics(rows, baseline_decisions(rows, pairs, books, name, task, policy[task]["threshold"]), task) for task in TASKS} for name, policy in calibrated_baselines.items()}
    metrics_by_task = {task: metrics(rows, {k: v["action"] for k, v in decisions.items()}, task) for task in TASKS}
    excluded = [r for r in rows if not primary(r)]
    review = []
    for row in rows:
        decision = decisions[row["pair_id"]]
        if not primary(row) or decision["action"] == "defer" or decision["action"] != row["label"]:
            review.append({**row, "prediction": decision, "signals": pairs[row["pair_id"]]})
    return {"schema": VERSION, "samples": len(rows), "label_counts": dict(collections.Counter(r["label"] for r in rows)),
            "primary_samples": sum(primary(r) for r in rows), "primary_policy": PRIMARY_POLICY,
            "improved": metrics_by_task, "point_precision_target_status": {task: m["precision"] is not None and m["precision"] >= .95 and m["positive_action_predictions"] >= 10 for task, m in metrics_by_task.items()}, "conditional_series": metrics(rows, {k: v["action"] for k, v in decisions.items()}, "series", True),
            "baseline_thresholds": BASE_THRESHOLDS, "baseline_note": BASELINE_NOTE, "baselines": baseline,
            "calibrated_single_signal_baselines": {"policy": calibrated_baselines, "metrics": calibrated_measured, "scope": "single_signal_reference_not_optimal_model_or_population_superiority"},
            "parameters": parameters, "score_semantics": "bounded_matching_signals_not_calibrated_probabilities",
            "mutually_exclusive_actions": True, "automatic_transitive_group_expansion": False,
            "defer_definition": "action=defer only; deferred positives count as false negatives; other action predictions count as task negatives",
            "excluded": {"rows": len(excluded), "uncertain": sum(r["label"] == "uncertain" for r in rows),
                         "partial_overlap": sum(r["collection_or_partial_overlap"] for r in rows),
                         "label_action_counts": dict(collections.Counter(r["label"] + ":" + decisions[r["pair_id"]]["action"] for r in excluded))},
            "missing_features": {name: len(books)-len(values) for name, values in features.items()},
            "candidate_coverage": retrieval(rows, books, structures, features, parameters),
            "predictions": [{**row, "prediction": decisions[row["pair_id"]]} for row in rows], "review_items": review,
            "label_triangle_reviews": label_consistency(rows),
            "limitations": ["candidate_selected_sample_not_population_accuracy", "semantic_family_audit_incomplete",
                           "95_percent_point_precision_target_not_guarantee", "pair_level_Wilson_ignores_within_family_dependence_not_family_population_guarantee", "Mac_cached_analysis_not_N5105_validation",
                           "cover_similarity_cannot_establish_full_content_identity", "existing_organization_not_used_as_feature_or_truth"]}


def input_hashes(directory):
    paths = [directory / name for name in ("analysis-books.json", "calibration.json", "input-audit.json")]
    paths += [directory.parent / ("features-" + name + ".json") for name in FEATURE_VERSIONS]
    return {str(path.relative_to(directory.parent)): file_digest(path) for path in paths}


def code_hashes():
    return {name: file_digest(Path(__file__).parent / name) for name in CODE_FILES}


def resource_summary(started, pairs, cpu_started):
    rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    rss = int(rss if platform.system() == "Darwin" else rss * 1024)
    return {"host": platform.system() + "/" + platform.machine(), "nas_verified": False, "cpu_threads_requested": 1,
            "model_inference_count": 0, "model_load_count": 0, "cached_analysis_wall_seconds": time.monotonic()-started,
            "cached_analysis_cpu_seconds": time.process_time()-cpu_started, "peak_rss_bytes": rss, "soft_rss_target_bytes": 2*1024**3,
            "rss_target_met": rss <= 2*1024**3, "labeled_pairs": pairs,
            "incremental_cost_note": "cached feature pair classification only; no model inference measured"}


def run(mode, directory=DIRECTORY):
    reject_symlinks(directory)
    directory.mkdir(mode=0o700, parents=True, exist_ok=True); os.chmod(directory, 0o700)
    freeze_path = directory / "frozen-policy.json"
    if mode == "calibrate" and freeze_path.exists():
        raise ValueError("already_frozen_no_recalibration")
    started, cpu_started = time.monotonic(), time.process_time()
    if mode == "evaluate":
        frozen = read_json(freeze_path)
        if frozen["code_sha256"] != code_hashes() or frozen["input_sha256"] != input_hashes(directory):
            raise ValueError("frozen_code_or_input_changed")
        if frozen["python"] != platform.python_version() or frozen["numpy"] != np.__version__:
            raise ValueError("frozen_runtime_version_changed")
    books, calibration, audit, features, metadata = load_inputs(directory)
    aliases = build_aliases(books)
    structures = {i: structure(book, aliases) for i, book in books.items()}
    rows = calibration["rows"]
    if mode == "evaluate":
        heldout = read_json(directory / "heldout.json")
        rows = validate_rows(heldout, books, "recommendation-heldout-v2")
        if heldout.get("frozen_policy_sha256") != file_digest(freeze_path):
            raise ValueError("heldout_release_does_not_match_frozen_policy")
        if heldout["pack_id"] != calibration["pack_id"] or heldout["feature_input_id"] != calibration["feature_input_id"] or len(rows) != audit["heldout_pairs"]:
            raise ValueError("heldout_identity_mismatch")
        cal_books = {i for r in calibration["rows"] for i in r["book_ids"]}
        cal_families = {f for r in calibration["rows"] for f in r["family_ids"]}
        if any(cal_books & set(r["book_ids"]) or cal_families & set(r["family_ids"]) for r in rows):
            raise ValueError("heldout_calibration_isolation_failure")
        parameters = frozen["parameters"]
        calibrated_baselines = frozen["calibrated_single_signal_baselines"]
        # Create the single-use marker only after all input and release checks pass.
        fd = os.open(directory / "heldout-evaluation.started", os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        os.close(fd)
    pairs = {r["pair_id"]: signals(*r["book_ids"], structures, features, books) for r in rows}
    if mode == "calibrate":
        parameters, trials = grid_search(rows, pairs)
        calibrated_baselines = calibrate_single_signals(rows, pairs, books)
    decisions = {r["pair_id"]: predict(pairs[r["pair_id"]], parameters) for r in rows}
    incremental_start = time.perf_counter()
    for _ in range(100):
        for signal in pairs.values():
            predict(signal, parameters)
    incremental_seconds = time.perf_counter() - incremental_start
    result = report(rows, pairs, decisions, parameters, books, structures, features, calibrated_baselines)
    result["resources"] = resource_summary(started, len(rows), cpu_started)
    result["resources"]["cached_pair_decision_median_proxy_us"] = incremental_seconds / max(1, len(rows)*100) * 1e6
    result["stage"] = mode
    if mode == "calibrate":
        result["grid"] = trials
        atomic_json(directory / "calibration-report.json", result)
        frozen = {"schema": "recommendation-frozen-policy-v2", "analysis_version": VERSION, "parameters": parameters,
                  "finite_grid": GRID, "selection_rule": "both_actions_precision_at_least_.95_and_at_least_10_positive_predictions_then_max_sum_recall_then_fewer_FP_then_more_TP_then_conservative_ties;fallback_fixed_.90_.84_.86_.04",
                  "baseline_thresholds": BASE_THRESHOLDS, "baseline_note": BASELINE_NOTE,
                  "calibrated_single_signal_baselines": calibrated_baselines,
                  "primary_policy": PRIMARY_POLICY, "score_semantics": result["score_semantics"], "defer_definition": result["defer_definition"],
                  "code_sha256": code_hashes(), "input_sha256": input_hashes(directory), "feature_metadata": metadata,
                  "alias_rule": "repeated_nested_group_artist_prefix_with_unanimous_nonempty_creator_tag_intersection_or_direct_exact_creator",
                  "all_other_numeric_and_regex_parameters": "fixed_in_code_sha256; no runtime defaults outside hashed code",
                  "aliases_sha256": digest(aliases), "pack_id": calibration["pack_id"], "feature_input_id": calibration["feature_input_id"],
                  "python": platform.python_version(), "numpy": np.__version__, "automatic_group_expansion": False,
                  "heldout_interface": {"path": "heldout.json", "schema": "recommendation-heldout-v2", "fields": ["schema", "pack_id", "feature_input_id", "frozen_policy_sha256", "rows"],
                     "row_fields": ["pair_id", "book_ids", "family_ids", "label", "reason", "collection_or_partial_overlap"],
                     "rows": audit["heldout_pairs"], "single_execution": True},
                  "calibration_report_sha256": file_digest(directory / "calibration-report.json")}
        atomic_json(freeze_path, frozen)
    else:
        result["frozen_policy_sha256"] = file_digest(freeze_path)
        result["heldout_sha256"] = file_digest(directory / "heldout.json")
        atomic_json(directory / "heldout-report.json", result)
    # Standard output contains aggregates only; individual IDs/titles stay private.
    print(json.dumps({"stage": mode, "samples": len(rows), "primary_samples": result["primary_samples"], "parameters": parameters,
                      "improved": result["improved"], "resources": result["resources"]}, ensure_ascii=False))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("calibrate", "evaluate"))
    args = parser.parse_args()
    try:
        run(args.mode)
    except (ValueError, OSError) as error:
        # Do not print private exception text or source records.
        print(json.dumps({"status": "failed", "category": type(error).__name__}), file=sys.stderr)
        raise SystemExit(1)
