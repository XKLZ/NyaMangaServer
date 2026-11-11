"""Versioned title/ordinal views. No labels, SQL, HTTP or inference."""
from __future__ import annotations
import re
from decimal import Decimal
import baseline as v2
import titles as title_v3

VERSION = 'series-title-views-v1'
PROTECTED = re.compile(r'\[[^\]]*\]|\([^)]*\)|【[^】]*】')


def parse(book, aliases):
    prior = title_v3.structure(book, aliases)
    text = prior['clean_text']
    protected = [(m.start(), m.end()) for m in PROTECTED.finditer(text)]
    pieces, last, positions = [], 0, []
    for match in v2.ORDINAL.finditer(text):
        if any(a <= match.start() < b for a, b in protected):
            continue
        if match['section']:
            token = match['section']
            kind = 'section_ud' if token in ('上', '中', '下') else 'section_fb'
            value = {'上': '1', '中': '2', '下': '3', '前': '1', '後': '2', '后': '2'}[token]
        else:
            value = v2.number(match['n'] or match['cn'])
            kind = 'volume' if (match['latin'] or '').startswith('vol') or match['unit'] in ('巻', '卷') else 'chapter'
        if value is None:
            continue
        positions.append({'kind': kind, 'value': value, 'ambiguous': False, 'text': match[0]})
        pieces.extend([text[last:match.start()], ' ']); last = match.end()
    pieces.append(text[last:])
    stem_text = re.sub(r'\s+', ' ', ''.join(pieces)).strip()
    main = re.split(r'\s+[-–—:：~～]\s+', stem_text, maxsplit=1)[0].strip()
    # Bare suffixes are auxiliary evidence. They never erase intrinsic identity.
    tentative = prior['tentative_number']
    if not positions and tentative is not None:
        positions.append({'kind': 'tentative_suffix', 'value': tentative, 'ambiguous': True, 'text': ''})
    return {**prior, 'series_stem': v2.key(stem_text), 'series_main': v2.key(main),
            'stem_text': stem_text, 'positions': positions, 'series_parse_version': VERSION}


def title_views(book, aliases):
    views = []
    for text in (book.get('title_jpn'), book.get('title')):
        if not text or any(v2.norm(text) == v2.norm(v['raw_text']) for v in views):
            continue
        views.append(parse({**book, 'title_jpn': text}, aliases))
    return views


def compare(a, b):
    same_tentative = bool(len(a['tentative_stem']) >= 4 and a['tentative_stem'] == b['tentative_stem'] and
                          (a['tentative_number'] is not None or b['tentative_number'] is not None))
    prefix = bool(len(a['number_prefix']) >= 5 and a['number_prefix'] == b['number_prefix'])
    title = max(v2.jac(a['stem_grams'], b['stem_grams']),
                v2.jac(v2.grams(a['series_stem']), v2.grams(b['series_stem'])),
                v2.jac(v2.grams(a['series_main']), v2.grams(b['series_main'])),
                .98 if same_tentative else 0, .88 if prefix else 0)
    explicit_a = {(p['kind'], p['value']) for p in a['positions'] if not p['ambiguous']}
    explicit_b = {(p['kind'], p['value']) for p in b['positions'] if not p['ambiguous']}
    distinct = bool(explicit_a and explicit_b and explicit_a != explicit_b) or (
        same_tentative and a['tentative_number'] != b['tentative_number']) or (
        a['core'] != b['core'] and a['series_stem'] != b['series_stem'])
    risks = []
    if explicit_a and explicit_b:
        if {k for k, _ in explicit_a} != {k for k, _ in explicit_b}:
            risks.append('ordinal_type_ambiguity')
        elif explicit_a == explicit_b:
            risks.append('same_position_needs_work_or_part_review')
    if a['series_main'] and b['series_main'] and title < .35:
        risks.append('different_title_subject')
    return {'title': title, 'distinct': distinct, 'risks': risks,
            'shared_subject': a['series_main'] if a['series_main'] == b['series_main'] else
            a['tentative_stem'] if same_tentative else '', 'positions': [a['positions'], b['positions']]}


def position(views):
    positions = {(p['kind'], p['value']) for v in views for p in v['positions'] if not p['ambiguous']}
    if len(positions) != 1:
        return None
    kind, value = next(iter(positions))
    return kind, Decimal(value)
