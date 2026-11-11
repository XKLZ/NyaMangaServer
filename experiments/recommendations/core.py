"""Independent, offline recommendation study. No application imports or writes."""
from __future__ import annotations

import collections
import contextlib
import hashlib
import itertools
import json
import os
from pathlib import Path, PurePosixPath
import re
import signal
import sqlite3
import stat
import tempfile
import unicodedata

VERSION = "recommendations-v1"
RULE_VERSION = "title-structure-v2-preserve-title-numbers"
REPO = Path(__file__).resolve().parents[2]
INPUT = REPO / ".runtime/tests/fixtures/treec-recommendations-input"
RUNTIME = REPO / ".runtime/tests/runs/recommendations"
CANCELLED = False


def on_cancel(signum, frame):
    global CANCELLED
    CANCELLED = True


def checkpoint():
    if CANCELLED:
        raise InterruptedError("cancelled")


def install_signals():
    signal.signal(signal.SIGTERM, on_cancel)
    signal.signal(signal.SIGINT, on_cancel)


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False,
                                     separators=(",", ":")).encode()).hexdigest()


def file_digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            checkpoint()
            h.update(block)
    return h.hexdigest()


def atomic_json(path, value):
    atomic_bytes(path, (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode())


def atomic_bytes(path, value):
    path = Path(path)
    reject_symlinks(path.parent)
    if path.is_symlink():
        raise ValueError("symlink_output")
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    fd, temporary = tempfile.mkstemp(prefix=".pending-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(value)
            f.flush()
            os.fsync(f.fileno())
        checkpoint()
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def private_output(path):
    path = Path(path).absolute()
    reject_symlinks(path)
    path = path.resolve()
    if not path.is_relative_to(RUNTIME.resolve()):
        raise ValueError("output_outside_experiment_runtime")
    RUNTIME.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(RUNTIME, 0o700)
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    for directory in (path, *path.parents):
        if directory.is_relative_to(RUNTIME):
            os.chmod(directory, 0o700)
    return path


def reject_symlinks(path):
    for part in [Path(path), *Path(path).parents]:
        if part.is_symlink():
            raise ValueError("symlink_path_component")


def cover_file(root, value):
    """Only map the suffix into the selected copy; never dereference old paths."""
    if not value:
        return None
    pp = PurePosixPath(value)
    parts = pp.parts
    if (".." in parts or len(parts) < 3 or parts[-3] != "Covers"
            or not re.fullmatch(r"[0-9a-f]{2}", parts[-2])
            or not re.fullmatch(r"[0-9a-f]{40}\.jpg", parts[-1])
            or parts[-2] != parts[-1][:2]):
        raise ValueError("nonstandard_cover_reference")
    cover_root = Path(root) / "Covers"
    target = cover_root / parts[-2] / parts[-1]
    for directory in (Path(root), cover_root, target.parent):
        if not stat.S_ISDIR(directory.lstat().st_mode):
            raise ValueError("unsafe_cover_directory")
    if not stat.S_ISREG(target.lstat().st_mode):
        raise ValueError("nonregular_cover")
    if not target.resolve().is_relative_to(cover_root.resolve()):
        raise ValueError("cover_escape")
    if target.stat().st_size > 10 * 1024 * 1024:
        raise ValueError("cover_byte_limit")
    return target


@contextlib.contextmanager
def source_connection(root):
    db = Path(root) / "TreeC.sqlite"
    reject_symlinks(db)
    if not stat.S_ISREG(db.lstat().st_mode) or Path(root).is_symlink():
        raise ValueError("unsafe_database")
    c = sqlite3.connect(db.as_uri() + "?mode=ro", uri=True, timeout=3)
    try:
        c.execute("PRAGMA query_only=ON")
        c.execute("PRAGMA trusted_schema=OFF")
        c.execute("BEGIN")
        yield c
    finally:
        c.rollback()
        c.close()


def read_source(root=INPUT):
    """Whitelist fields; never read filepath, URLs, credentials, vectors or raw blobs."""
    with source_connection(root) as c:
        if c.execute("PRAGMA quick_check").fetchall() != [("ok",)]:
            raise ValueError("database_quick_check_failed")
        if c.execute("PRAGMA foreign_key_check").fetchone():
            raise ValueError("database_foreign_keys_failed")
        migrations = [r[0] for r in c.execute("SELECT identifier FROM grdb_migrations ORDER BY rowid")]
        if not migrations or migrations[-1] != "v21_work_character_claims":
            raise ValueError("unsupported_source_schema")
        c.row_factory = sqlite3.Row
        columns = "id,title,title_jpn,cover_path,cover_hash,hash,pages,collection_id,collection_order,version_group_id,is_primary_version,language_code,is_translated,is_full_color,censorship,has_extraneous_ads"
        books = {r["id"]: dict(r) for r in c.execute("SELECT " + columns + " FROM library_books ORDER BY id")}
        galleries = {r["hash"]: r["gallery_id"] for r in c.execute("SELECT hash,gallery_id FROM gallery_metadata")}
        for b in books.values():
            b["tags"] = []
            b["gallery_id_sampling_only"] = galleries.get(b["hash"])
        for r in c.execute("SELECT bt.book_id,t.namespace,t.name FROM book_tags bt JOIN tags t ON t.id=bt.tag_id ORDER BY bt.book_id,t.namespace,t.name"):
            if r["book_id"] in books:
                books[r["book_id"]]["tags"].append([r["namespace"], r["name"]])
    fingerprint = digest({"migrations": migrations, "books": list(books.values())})
    return books, {"schema": migrations[-1], "migration_count": len(migrations),
                   "input_fingerprint": fingerprint, "books": len(books),
                   "copy_stopped_writes_confirmed": False,
                   "consistency_note": "quick_check is structural; copy completion after stop remains unconfirmed"}


def normalize(text):
    return unicodedata.normalize("NFKC", text or "").casefold().strip()


def title_structure(text):
    text = normalize(text)
    decorations = r"(?:english|chinese|japanese|translated|digital|full[ -]?colou?r|uncensored|カラー|無修正|修正版|漢化|汉化|翻訳|dl版)"
    def bracket(match):
        inner = match.group(0)[1:-1].strip()
        return " " if re.fullmatch(decorations, inner) else match.group(0)
    # Unknown brackets may be part of the true title; preserve their contents.
    text = re.sub(r"\[[^\]]*\]|\([^)]*\)|【[^】]*】", bracket, text)
    explicit = []
    pattern = r"\b(vol(?:ume)?\.?|chapter|ch\.?)\s*(\d{1,4})\b|(?:第\s*)?(\d{1,4})\s*(巻|卷|話|话|章)"
    def ordinal(match):
        marker = match.group(1) or match.group(4)
        number = int(match.group(2) or match.group(3))
        kind = "volume" if marker.startswith("vol") or marker in ("巻", "卷") else "chapter"
        explicit.append([kind, number])
        return " "
    text = re.sub(pattern, ordinal, text)
    core = "".join(ch for ch in text if ch.isalnum())
    grams = {core[i:i + 2] for i in range(max(0, len(core) - 1))}
    return {"core": core, "grams": grams, "volumes": explicit}


def book_structure(b):
    return title_structure(b.get("title_jpn") or b.get("title"))


def jaccard(a, b):
    return len(a & b) / len(a | b) if a and b else 0.0


def rules(a, b):
    sa, sb = book_structure(a), book_structure(b)
    creator_a = {tuple(t) for t in a["tags"] if t[0] in ("artist", "group")}
    creator_b = {tuple(t) for t in b["tags"] if t[0] in ("artist", "group")}
    title_score = jaccard(sa["grams"], sb["grams"])
    same_core = bool(sa["core"] and sa["core"] == sb["core"])
    volume_conflict = bool(sa["volumes"] and sb["volumes"] and sa["volumes"] != sb["volumes"])
    shared_creator = bool(creator_a & creator_b)
    pages_a, pages_b = a.get("pages") or 0, b.get("pages") or 0
    page_ratio = min(pages_a, pages_b) / max(pages_a, pages_b) if pages_a and pages_b else None
    reasons = []
    if same_core:
        reasons.append("same_normalized_title_core")
    if shared_creator:
        reasons.append("shared_creator_tag")
    if volume_conflict:
        reasons.append("different_explicit_volume_or_chapter")
    version_score = 0.7 * title_score + 0.2 * shared_creator + 0.1 * (page_ratio or 0)
    series_score = 0.75 * title_score + 0.25 * shared_creator
    return {"version": RULE_VERSION, "title_jaccard": title_score,
            "version_score": round(version_score, 6), "series_score": round(series_score, 6),
            "page_ratio": page_ratio, "volume_conflict": volume_conflict,
            "version_blocked": volume_conflict, "same_core": same_core,
            "shared_creator": shared_creator, "reasons": reasons,
            "gallery_id_is_evidence": False}


def families(books, calibration_percent=70):
    """Conservative known-family isolation; no similarity transitive expansion."""
    parent = {i: i for i in books}
    def find(i):
        while parent[i] != i:
            parent[i] = parent[parent[i]]
            i = parent[i]
        return i
    def union(a, b):
        pa, pb = find(a), find(b)
        parent[max(pa, pb)] = min(pa, pb)
    buckets = {}
    for i, b in books.items():
        keys = [("group", b["version_group_id"]), ("series", b["collection_id"])]
        core = book_structure(b)["core"]
        if len(core) >= 4:
            keys.append(("exact_title_core", core))
        for key in keys:
            if key[1] is None:
                continue
            if key in buckets:
                union(i, buckets[key])
            else:
                buckets[key] = i
    result = {i: "family-" + digest(find(i))[:16] for i in books}
    split = {i: ("calibration" if int(digest(result[i])[:8], 16) % 100 < calibration_percent else "test") for i in books}
    return result, split


def select_pairs(books):
    family, splits = families(books)
    pools = {task: {kind: [] for kind in ("positive_candidate", "hard_negative_candidate", "ordinary_negative_candidate")} for task in ("version", "series")}
    # Existing organization is a sampling hint, deliberately never a label.
    for task, field in (("version", "version_group_id"), ("series", "collection_id")):
        buckets = collections.defaultdict(list)
        for i, b in books.items():
            if b[field] is None:
                continue
            if task == "series" and b["version_group_id"] is not None and not b["is_primary_version"]:
                continue
            buckets[b[field]].append(i)
        for ids in buckets.values():
            for a, b in itertools.combinations(ids, 2):
                if task == "series" and books[a]["version_group_id"] is not None and books[a]["version_group_id"] == books[b]["version_group_id"]:
                    continue
                pools[task]["positive_candidate"].append((a, b, "existing_organization_only"))
    structures = {i: book_structure(b) for i, b in books.items()}
    inverted = collections.defaultdict(set)
    creators = collections.defaultdict(set)
    ambiguous_galleries = collections.defaultdict(set)
    identical_covers = collections.defaultdict(set)
    for i, b in books.items():
        for gram in structures[i]["grams"]:
            inverted[gram].add(i)
        for ns, value in b["tags"]:
            if ns in ("artist", "group"):
                creators[(ns, value)].add(i)
        if b.get("gallery_id_sampling_only") is not None:
            ambiguous_galleries[b["gallery_id_sampling_only"]].add(i)
        if b.get("cover_hash"):
            identical_covers[b["cover_hash"]].add(i)
    seen = set()
    for a in books:
        checkpoint()
        nearby = collections.Counter()
        for gram in structures[a]["grams"]:
            if len(inverted[gram]) <= 250:
                nearby.update(inverted[gram])
        for ns, value in books[a]["tags"]:
            if ns in ("artist", "group") and len(creators[(ns, value)]) <= 150:
                nearby.update(creators[(ns, value)])
        forced = ambiguous_galleries.get(books[a].get("gallery_id_sampling_only"), set()) | identical_covers.get(books[a].get("cover_hash"), set())
        for b in set(i for i, _ in nearby.most_common(35)) | forced:
            pair = tuple(sorted((a, b)))
            if a == b or pair in seen or splits[a] != splits[b]:
                continue
            seen.add(pair)
            r = rules(books[a], books[b])
            for task, field in (("version", "version_group_id"), ("series", "collection_id")):
                if books[a][field] is not None and books[a][field] == books[b][field]:
                    continue
                if task == "series" and books[a]["version_group_id"] is not None and books[a]["version_group_id"] == books[b]["version_group_id"]:
                    continue
                hints = []
                if books[a].get("gallery_id_sampling_only") is not None and books[a].get("gallery_id_sampling_only") == books[b].get("gallery_id_sampling_only"):
                    hints.append("same_gallery_different_organization_not_identity_evidence")
                if books[a].get("cover_hash") and books[a].get("cover_hash") == books[b].get("cover_hash"):
                    hints.append("identical_cached_cover_not_content_identity")
                if r["volume_conflict"] and r["shared_creator"]:
                    hints.append("same_creator_different_explicit_volume")
                if r["title_jaccard"] >= 0.12 or r["shared_creator"] or hints:
                    pools[task]["hard_negative_candidate"].append((*pair, ";".join(hints) or "near_title_or_creator_unconfirmed"))
    ids = sorted(books, key=lambda i: digest([VERSION, i]))
    for offset in (1, 7, 31, 103):
        for index, a in enumerate(ids):
            b = ids[(index + offset) % len(ids)]
            if a == b or splits[a] != splits[b] or family[a] == family[b]:
                continue
            a, b = sorted((a, b))
            r = rules(books[a], books[b])
            if r["title_jaccard"] > 0.08 or r["shared_creator"]:
                continue
            for task, field in (("version", "version_group_id"), ("series", "collection_id")):
                if books[a][field] is not None and books[a][field] == books[b][field]:
                    continue
                pools[task]["ordinary_negative_candidate"].append((a, b, "dissimilar_title_and_creator_unconfirmed"))
    selected = []
    for task in pools:
        used = set()
        for split, counts in (("calibration", (14, 10, 4)), ("test", (6, 5, 1))):
            for kind, count in zip(pools[task], counts):
                candidates = [p for p in pools[task][kind] if splits[p[0]] == splits[p[1]] == split]
                candidates.sort(key=lambda p: (-(rules(books[p[0]], books[p[1]])[task + "_score"] if kind == "hard_negative_candidate" else 0), digest([task, kind, *p[:2]])))
                if kind == "hard_negative_candidate":
                    # Include explicit confounders before rank-based hard examples.
                    preferred = []
                    for hint in ("same_gallery_", "identical_cached_cover", "same_creator_different_explicit_volume"):
                        preferred.extend([p for p in candidates if hint in p[2]][:2])
                    candidates = preferred + candidates
                taken = 0
                family_used = collections.Counter()
                # Favor family diversity; fall back only if the pool requires it.
                for cap in (1, 2, 40):
                    for a, b, hint in candidates:
                        if (a, b) in used or max(family_used[family[a]], family_used[family[b]]) >= cap:
                            continue
                        used.add((a, b));family_used.update(set((family[a], family[b])))
                        selected.append({"pair_id": task + "-" + digest([a, b])[:16], "task": task,
                                         "book_ids": [a, b], "family_ids": sorted(set((family[a], family[b]))),
                                         "split": split, "stratum": kind, "selection_hint": hint,
                                         "label": None, "reason": "", "rules": rules(books[a], books[b])})
                        taken += 1
                        if taken == count:
                            break
                    if taken == count:
                        break
                if taken != count:
                    raise ValueError("insufficient_candidate_pool:" + task + ":" + split + ":" + kind)
    cal = {f for p in selected if p["split"] == "calibration" for f in p["family_ids"]}
    test = {f for p in selected if p["split"] == "test" for f in p["family_ids"]}
    assert not cal & test
    return selected, {"family_version": "organization-and-exact-core-v2-70-30", "calibration_pair_fraction": 0.7, "known_family_overlap": 0,
                       "families": len(set(family.values())), "calibration_families": len(cal),
                       "test_families": len(test), "unknown_semantic_family_leakage": "requires_human_audit"}


def validate_labels(pack, labels):
    if not isinstance(labels, dict) or labels.get("schema") != "recommendation-labels-v1" or not isinstance(labels.get("annotations"), list):
        raise ValueError("invalid_annotation_schema")
    if labels.get("pack_id") != pack["pack_id"]:
        raise ValueError("annotation_pack_mismatch")
    valid = {p["pair_id"] for p in pack["pairs"]}
    seen = set()
    for row in labels.get("annotations", []):
        if not isinstance(row, dict):
            raise ValueError("invalid_annotation_row")
        pair = row.get("pair_id")
        if not isinstance(pair, str) or pair not in valid or pair in seen:
            raise ValueError("unknown_or_duplicate_annotation")
        if "label" not in row or row["label"] not in (None, "same", "different", "uncertain"):
            raise ValueError("invalid_annotation_label")
        if not isinstance(row.get("reason"), str) or len(row["reason"]) > 4000:
            raise ValueError("invalid_annotation_reason")
        seen.add(pair)
    return labels
