"""Pinned V2 pure matching route, retained for the V4 production pilot.

No calibration, source database access, experiment imports or series operations.
"""
import collections
import html
import re
import unicodedata
import numpy as np

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


def signals(a, b, structures, features, books, scores_override=None):
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
    scores = features_for_pair(a, b, features) if scores_override is None else scores_override
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

