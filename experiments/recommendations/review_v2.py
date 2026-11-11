"""Blind four-relation booklet: only a display whitelist is embedded."""
import base64
import io
import json
from pathlib import Path
from core import atomic_bytes, cover_file, checkpoint


def display_data(pack, books, source):
    from features import safe_image
    from PIL import Image
    display = {};errors = missing = 0
    for i in sorted({i for p in pack['pairs'] for i in p['book_ids']}):
        checkpoint()
        b = books[i];encoded = None;status = '可用'
        try:
            path = cover_file(source, b['cover_path'])
            if path is None:
                missing += 1;status = '没有封面引用'
            else:
                image = safe_image(path)
                image.thumbnail((420, 560), Image.Resampling.LANCZOS)
                buffer = io.BytesIO();image.save(buffer, 'JPEG', quality=82, optimize=True)
                encoded = 'data:image/jpeg;base64,' + base64.b64encode(buffer.getvalue()).decode()
        except (OSError, ValueError, Warning):
            errors += 1;status = '封面不可用'
        display[str(i)] = {'title': b['title'], 'title_jpn': b['title_jpn'], 'pages': b['pages'],
                           'language': b['language_code'], 'image': encoded, 'image_status': status,
                           'tags': [ns + ':' + name for ns, name in b['tags']]}
    # Never embed the private sampling pools, scores, original groups or split IDs.
    pairs = [{'pair_id': p['pair_id'], 'book_ids': p['book_ids'], 'batch': p['batch']} for p in pack['pairs']]
    return {'pack_id': pack['pack_id'], 'pairs': pairs, 'books': display}, {
        'displayed_books': len(display), 'image_mapping_errors': errors, 'missing_cover_references': missing}


def render(pack, books, source, output):
    data, summary = display_data(pack, books, source)
    root = Path(__file__).parent
    template = (root / 'review_v2.html').read_text()
    javascript = (root / 'review_v2.js').read_text()
    payload = json.dumps(data, ensure_ascii=False).replace('<', '\\u003c').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')
    page = template.replace('__SCRIPT__', javascript).replace('__DATA__', payload)
    atomic_bytes(output, page.encode())
    return summary
