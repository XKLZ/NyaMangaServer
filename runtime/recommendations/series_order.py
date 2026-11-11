"""Explicit sequence evidence; does not change an existing Series order."""
from series_titles import position

def order_plan(ids, profiles, existing=None):
    positions = {i: position(profiles[i]['views']) for i in ids}
    values = list(positions.values())
    valid = all(v is not None for v in values) and len({v[0] for v in values if v}) == 1 and len(set(values)) == len(values)
    if not valid:
        return {'work_ids': list(existing) if existing else sorted(ids), 'suggested_order': None,
                'state': 'manual_order_required', 'reason': 'missing_mixed_or_duplicate_position'}
    ordered = sorted(ids, key=lambda i: (positions[i][1], i))
    if existing and [i for i in ordered if i in existing] != existing:
        return {'work_ids': list(existing), 'suggested_order': None, 'state': 'manual_order_required', 'reason': 'preserve_existing_order'}
    return {'work_ids': ordered, 'suggested_order': ordered, 'state': 'explicit_position_order', 'reason': None}
