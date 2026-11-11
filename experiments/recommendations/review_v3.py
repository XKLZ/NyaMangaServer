"""V3 booklet rendering; preserve the original V2 templates and source."""
import json
import re
from pathlib import Path
from core import atomic_bytes
from review_v2 import display_data


def render(pack, books, source, output, labels_filename='labels.user.v3.json'):
    if not re.fullmatch(r'[a-zA-Z0-9._-]+\.json', labels_filename):
        raise ValueError('unsafe_annotation_filename')
    data, summary = display_data(pack, books, source)
    root = Path(__file__).parent
    template = (root / 'review_v3.html').read_text()
    javascript = (root / 'review_v2.js').read_text()
    old_filename = "link.download='labels.user.v2.json'"
    if javascript.count(old_filename) != 1:
        raise ValueError('incompatible_shared_booklet_script')
    javascript = javascript.replace(old_filename, 'link.download=' + json.dumps(labels_filename))
    template = template.replace('__PAIR_COUNT__', str(len(pack['pairs'])))
    template = template.replace('__BATCH_COUNT__', str((len(pack['pairs']) + 29) // 30))
    payload = json.dumps(data, ensure_ascii=False).replace('<', '\\u003c').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')
    page = template.replace('__SCRIPT__', javascript).replace('__DATA__', payload)
    atomic_bytes(output, page.encode())
    return summary
