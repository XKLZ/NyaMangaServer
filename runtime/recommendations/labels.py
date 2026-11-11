"""Conservative, deterministic Work-local preset suggestions; no model or I/O.

TreeC's unattributed default booleans and custom labels are display-only. Every
inferred positive/negative needs explicit evidence. Unknown never proves contrast.
"""
from __future__ import annotations
import re
import unicodedata

VERSION = 'edition-preset-labels-evidence-v2'
PRESETS = {'original': '原版', 'translated': '译本', 'monochrome': '黑白', 'color': '彩色', 'censored': '修正', 'uncensored': '无修', 'special': '特别', 'dl': 'DL'}
DIMENSIONS = ('translation', 'censorship', 'color', 'dl', 'special')
VALUES = {'translation': {'original', 'translated'}, 'color': {'monochrome', 'color'}, 'censorship': {'censored', 'uncensored'}, 'dl': {'dl'}, 'special': {'special'}}
MARKERS = {
    'translation': {'translated': {'translated', 'translation', '汉化', '漢化', '翻訳', '英訳'}, 'original': {'untranslated', '未翻译', '未翻譯', '未翻訳'}},
    'color': {'color': {'full color', 'full colour', 'color', 'colour', '全彩', '彩色', 'カラー', 'カラー化'}, 'monochrome': {'monochrome', 'black and white', '黑白', '白黒', '白黑'}},
    'censorship': {'censored': {'censored', 'mosaic', 'mosaic censorship', 'full censorship', '有修', '有修正', '马赛克', '馬賽克'}, 'uncensored': {'uncensored', '無修正', '无修正', '無修', '无修', '无码', '無碼'}},
    'dl': {'dl': {'dl', 'digital', 'digital edition', 'digital version', '電子版', '电子版', '電子書籍', '电子书'}},
    'special': {'special': {'special edition', 'extended edition', '特別版', '特别版', '增补版', '增補版', '増補版'}},
}
BRACKETS = re.compile(r'\[([^\[\]]+)\]|\(([^()]+)\)|【([^【】]+)】|（([^（）]+)）|［([^［］]+)］|「([^「」]+)」|『([^『』]+)』')
LANGUAGE_STATUS = {'unknown', 'textless narrative', 'translated', 'translation', 'untranslated', 'rewrite', 'speechless', 'text cleaned', 'sample', '汉化', '漢化', '翻訳'}


def norm(value):
    return re.sub(r'\s+', ' ', unicodedata.normalize('NFKC', value).casefold()).strip()


def languages(book):
    values = book.get('languages')
    if values is None:
        values = [name for namespace, name in book.get('tags', []) if namespace == 'language' and norm(name) not in LANGUAGE_STATUS]
        if not values and book.get('language_code') and norm(book['language_code']) not in LANGUAGE_STATUS:
            values = [book['language_code']]
    return sorted({norm(v) for v in values if isinstance(v, str) and norm(v) and norm(v) not in LANGUAGE_STATUS})


def infer(book):
    result = {}; title_markers = []
    for field in ('title', 'title_jpn'):
        text = book.get(field) or ''
        for match in BRACKETS.finditer(text):
            title_markers.append((field, norm(next(v for v in match.groups() if v is not None))))
        # Explicit edition suffix only, never arbitrary 'special' prose.
        suffix = re.search(r'(special edition|extended edition|特別版|特别版|增补版|增補版|増補版)\s*$', norm(text))
        if suffix: title_markers.append((field, suffix[1]))
        suffix = re.search(r'(?:^|[\s_-])(dl|digital(?: edition| version)?|電子版|电子版|電子書籍|电子书)$',norm(text))
        if suffix: title_markers.append((field,suffix[1]))
    for dimension in DIMENSIONS:
        hits = []
        manual_fields=book.get('manual_properties', {})
        manual = manual_fields.get({'translation':'translated','color':'color_mode'}.get(dimension,dimension),manual_fields.get(dimension))
        if manual is not None:
            value = manual.get('value')
            if dimension == 'translation' and type(value) is bool:value='translated' if value else 'original'
            if dimension == 'censorship' and value in ('mosaic','full'):value='censored'
            if value is not None and value not in VALUES[dimension]:
                raise ValueError('invalid_manual_preset_value')
            result[dimension] = {'value': value, 'origin': 'manual', 'state': 'known' if value else 'explicit_unknown', 'evidence': [{'kind': 'manual_property', 'value': value}]}
            continue
        for namespace, name in book.get('tags', []):
            # Translation is a language status, not a creator named Translated.
            allowed = {'language'} if dimension == 'translation' else {'other', 'misc'}
            if namespace not in allowed: continue
            for value, markers in MARKERS[dimension].items():
                if norm(name) in markers:
                    hits.append({'kind': 'tag', 'namespace': namespace, 'name': name, 'value': value})
        for field, marker in title_markers:
            for value, markers in MARKERS[dimension].items():
                if marker in markers or (value == 'translated' and re.fullmatch(r'[\w\s・ー-]{0,64}(?:汉化|漢化)(?:组|組)?', marker)):
                    hits.append({'kind': 'title_marker', 'field': field, 'marker': marker, 'value': value})
        unique = {h['value'] for h in hits}
        value = next(iter(unique)) if len(unique) == 1 else None
        result[dimension] = {'value': value, 'origin': 'automatic', 'state': 'known' if value else 'conflict' if len(unique) > 1 else 'unknown', 'evidence': hits}
    return {'rule_version': VERSION, 'languages': languages(book), 'properties': result,
            'existing_custom_labels': list(book.get('custom_labels', [])), 'legacy_defaults_used_as_evidence': False}


def suggest_work(editions):
    """Keep reliable explicit evidence; no opposite inference or fallback label."""
    if not editions or len({e['id'] for e in editions}) != len(editions):
        raise ValueError('empty_or_duplicate_work_members')
    rows=[]
    for edition in editions:
        current=infer(edition)
        presets=[current['properties'][d]['value'] for d in DIMENSIONS
                 if current['properties'][d]['value'] in PRESETS] if len(editions)>1 else []
        rows.append({'edition_id':edition['id'],**current,
          'suggested_presets':[{'id':p,'label':PRESETS[p]} for p in presets],
          'unresolved_peer_ids':[], 'needs_manual_review':len(editions)>1 and not presets,
          'scope':'hypothetical_work_suggestion_not_written_or_identity_evidence',
          'preserves_existing_custom_labels':True})
    return {'schema':'edition-preset-suggestions-v4','rule_version':VERSION,'editions':rows,
            'work_member_ids':[e['id'] for e in editions],'automatic_write':False}
