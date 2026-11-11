"""Coordinator-only input isolation/release. Never calibrates or evaluates models."""
import argparse
import os
from pathlib import Path
from core import RUNTIME, atomic_json, file_digest, private_output
from sampling_v2 import read_json, validate_labels
from sampling_v3 import verify_v2_freeze

ROOT = RUNTIME / 'run-v3'
PREVIOUS = RUNTIME / 'run-v2'
BOOK_FIELDS = {'id', 'title', 'title_jpn', 'pages', 'tags', 'language_code',
               'is_translated', 'is_full_color', 'censorship', 'has_extraneous_ads'}
INPUT_FILES = ('books.json', 'rows.json', 'features-phash.json', 'features-sscd.json',
               'features-minilm.json', 'v2-baseline.json')


def immutable_json(path, value):
    if path.exists():
        if read_json(path) != value:
            raise ValueError('immutable_coordinator_material_changed:' + path.name)
    else:
        atomic_json(path, value)


def labeled_rows(pack, labels):
    validate_labels(pack, labels)
    annotations = {r['pair_id']: r for r in labels['annotations']}
    if any(r['label'] is None for r in annotations.values()):
        raise ValueError('annotation_incomplete')
    return [{'pair_id': p['pair_id'], 'book_ids': p['book_ids'], 'family_ids': p['family_ids'],
             **{k: annotations[p['pair_id']][k] for k in ('label', 'reason', 'collection_or_partial_overlap')}}
            for p in pack['pairs']]


def seal(root=ROOT):
    pack = read_json(root / 'candidates.json')
    path = root / 'labels.user.v3.json'
    labels = read_json(path)
    rows = labeled_rows(pack, labels)
    if len(rows) != 120:
        raise ValueError('expected_120_heldout_rows')
    os.chmod(path, 0o600)
    value = {'schema': 'recommendation-heldout-seal-v3', 'pack_id': pack['pack_id'],
             'label_sha256': file_digest(path), 'candidates_sha256': file_digest(root / 'candidates.json'),
             'pairs': 120, 'state': 'sealed_until_root_freeze_approval'}
    immutable_json(root / 'coordinator/heldout-seal.json', value)
    return pack, labels


def write_inputs(directory, books, pack, labels, source, baseline, evaluation=False):
    rows = labeled_rows(pack, labels)
    immutable_json(directory / 'books.json', {'schema': 'recommendation-books-v3', 'books': books})
    immutable_json(directory / 'rows.json', {'schema': 'recommendation-heldout-v3' if evaluation else 'recommendation-development-v3',
                                            'pack_id': pack['pack_id'], 'rows': rows})
    ids = {b['id'] for b in books}
    for algorithm in ('phash', 'sscd', 'minilm'):
        material = read_json(source / ('features-' + algorithm + '.json'))
        material['items'] = {k: v for k, v in material['items'].items() if int(k) in ids}
        immutable_json(directory / ('features-' + algorithm + '.json'), material)
    immutable_json(directory / 'v2-baseline.json', baseline)
    manifest = {'schema': 'recommendation-evaluation-manifest-v3' if evaluation else 'recommendation-development-manifest-v3',
                'pairs': len(rows), 'books': len(books), 'files': {name: file_digest(directory / name) for name in INPUT_FILES}}
    if not evaluation:
        manifest.update({'heldout_access': 'forbidden_until_root_approval',
                         'retrieval_pool_scope': 'development_selected_books_only_until_freeze;full_whitelist_released_after_freeze'})
    immutable_json(directory / 'input-manifest.json', manifest)


def prepare(root=ROOT, previous=PREVIOUS):
    verify_v2_freeze()
    seal(root)
    pack = read_json(previous / 'candidates.json');labels = read_json(previous / 'labels.user.v2.json')
    selected = {i for p in pack['pairs'] for i in p['book_ids']}
    books = [{k: v for k, v in b.items() if k in BOOK_FIELDS}
             for b in read_json(previous / 'books.json')['books'] if b['id'] in selected]
    frozen = read_json(previous / 'analysis/frozen-policy.json')
    baseline = {'parameters': frozen['parameters'], 'approved_policy_sha256': file_digest(previous / 'analysis/frozen-policy.json')}
    write_inputs(root / 'analysis/development', books, pack, labels, previous, baseline)


def release(approved_sha, root=ROOT, previous=PREVIOUS):
    """Call only after human-coordinator code review; no scoring occurs here."""
    directory = root / 'analysis'
    if (directory / 'heldout-evaluation.started').exists():
        raise ValueError('heldout_evaluation_already_started')
    path = directory / 'frozen-policy-v3.json'
    if not approved_sha or file_digest(path) != approved_sha:
        raise ValueError('explicit_approved_freeze_digest_required')
    verify_v2_freeze()
    pack, labels = seal(root)
    isolation = read_json(root / 'coordinator/isolation-audit.json')
    if isolation['new_pack_sha256'] != file_digest(root / 'candidates.json'):
        raise ValueError('isolation_audit_pack_changed')
    if any(isolation[k] for k in ('known_family_overlap', 'book_overlap', 'exact_cover_overlap')) or isolation['extended_exact_title_candidates']:
        raise ValueError('unresolved_cross_set_isolation')
    # The full whitelist is released only now, after calibration has frozen.
    books = [{k: v for k, v in b.items() if k in BOOK_FIELDS} for b in read_json(previous / 'books.json')['books']]
    baseline = read_json(directory / 'development/v2-baseline.json')
    target = directory / 'evaluation'
    write_inputs(target, books, pack, labels, previous, baseline, evaluation=True)
    immutable_json(directory / 'approval.json', {'frozen_policy_sha256': approved_sha, 'approved': True})
    immutable_json(directory / 'heldout.json', {'frozen_policy_sha256': approved_sha,
                   'input_manifest_sha256': file_digest(target / 'input-manifest.json'), 'expected_pairs': 120})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('command', choices=('prepare', 'release'))
    parser.add_argument('--approved-freeze-sha256')
    args = parser.parse_args()
    root = private_output(ROOT)
    if args.command == 'prepare': prepare(root)
    else: release(args.approved_freeze_sha256, root)
    print('coordinator_' + args.command + '_complete')


if __name__ == '__main__': main()
