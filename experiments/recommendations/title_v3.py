"""Independent V3 title views. Unknown decorations/numbers remain evidence."""
from __future__ import annotations
import re
from analyze_v2 import norm, key, grams, jac, build_aliases, structure as old_structure, creator_relation

VERSION = 'title-views-v3.1'
SUBTITLE = re.compile(r'\s+(?:[-–—:：]|~|～)\s+|[「『](.+?)[」』]')


def structure(book, aliases):
    old = old_structure(book, aliases)
    raw = book.get('title_jpn') or book.get('title') or ''
    # Preserve Unicode prose for the existing semantic encoder; keys are for exact/gram matching.
    from analyze_v2 import BRACKETS, EVENT, DECORATION, DATE, PREFIX, creator_sets
    text = norm(raw)
    removed = []
    def bracket(match):
        inside = match[0][1:-1].strip()
        if EVENT.fullmatch(inside) or DECORATION.fullmatch(inside) or DATE.fullmatch(inside):
            removed.append('recognized_modifier'); return ' '
        return match[0]
    text = BRACKETS.sub(bracket, text).strip()
    prefix = PREFIX.match(text)
    if prefix:
        signature = key(prefix[1]); creators = creator_sets(book)
        if any(signature in creators[ns] for ns in creators) or any(set(values) & creators[ns] for ns, values in aliases.get(signature, {}).items()):
            text = text[prefix.end():].strip(); removed.append('creator_prefix_tag_supported')
    clean = re.sub(r'\s+', ' ', text).strip()
    # Subtitles are a supplementary view, never discarded from clean text.
    parts = re.split(r'\s+[-–—:：~～]\s+', clean, maxsplit=1)
    main, subtitle = parts[0], parts[1] if len(parts) > 1 else ''
    positions = [{'kind': k, 'value': v, 'ambiguous': False} for k, v in old['ordinals']]
    if old['tentative_number'] is not None:
        positions.append({'kind': 'tentative_suffix', 'value': old['tentative_number'], 'ambiguous': True})
    return {**old, 'raw_text': raw, 'clean_text': clean, 'main_text': main, 'subtitle_text': subtitle,
            'positions': positions, 'parse_version': VERSION, 'clean_changed': clean != raw,
            'clean_removed': removed}


def pair_views(a, b):
    explicit_a = {(p['kind'], p['value']) for p in a['positions'] if not p['ambiguous']}
    explicit_b = {(p['kind'], p['value']) for p in b['positions'] if not p['ambiguous']}
    comparable = bool(explicit_a and explicit_b and {k for k, _ in explicit_a} == {k for k, _ in explicit_b})
    return {'main_similarity': jac(grams(key(a['main_text'])), grams(key(b['main_text']))),
            'explicit_ordinal_conflict': comparable and explicit_a != explicit_b,
            'ordinal_type_ambiguous': bool(explicit_a and explicit_b and not comparable),
            'position_missing_one_side': bool(explicit_a) != bool(explicit_b),
            'subtitle_similarity': jac(grams(key(a['subtitle_text'])), grams(key(b['subtitle_text']))),
            'positions': [a['positions'], b['positions']]}
