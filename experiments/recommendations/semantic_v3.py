"""Offline, versioned cleaned-title views using the already pinned MiniLM worker."""
from __future__ import annotations
import json
import os
import time
import numpy as np
from pathlib import Path
from core import atomic_json, digest, reject_symlinks, file_digest, checkpoint
from features import model_worker, check_resources
from models import offline_environment
from title_v3 import VERSION as PARSER_VERSION

VERSION = 'minilm-clean-title-128-mean-l2-v3.1'


def vector(value):
    result = np.asarray(value, dtype=np.float32)
    if result.shape != (384,) or not np.isfinite(result).all() or not .999 <= float(np.linalg.norm(result)) <= 1.001:
        raise ValueError('invalid_clean_semantic_vector')
    # Persisted float32 bytes are canonical. Re-normalization changes low bits.
    return result


def normalize_inference(value):
    result = np.asarray(value, dtype=np.float32)
    if result.shape != (384,) or not np.isfinite(result).all():
        raise ValueError('invalid_clean_semantic_vector')
    magnitude = float(np.linalg.norm(result))
    if magnitude < 1e-12:
        raise ValueError('invalid_clean_semantic_vector')
    return vector(result / magnitude)


def clean_features(books, structures, raw_features, output, model_sha, worker_factory=model_worker):
    offline_environment()
    cache = output / 'feature-cache-v3' / 'minilm'
    reject_symlinks(cache); cache.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(cache.parent, 0o700); os.chmod(cache, 0o700)
    infer = None; values = {}; hashes = {}; computed = reused = unchanged = 0; started = time.monotonic()
    for i in sorted(books):
        checkpoint()
        s = structures[i]
        if not s['clean_text']:
            continue
        if not s['clean_changed'] and i in raw_features:
            values[i] = raw_features[i]; unchanged += 1; continue
        # Match features.run's 8192-Unicode-character bound before tokenization
        # (at most 32768 UTF-8 bytes), then the pinned worker limits 128 tokens.
        actual_input = s['clean_text'][:8192]
        fingerprint = digest([VERSION, PARSER_VERSION, model_sha, actual_input])
        path = cache / (fingerprint + '.json'); reject_symlinks(path)
        if path.exists():
            item = json.loads(path.read_text())
            if item.get('fingerprint') != fingerprint or item.get('feature_digest') != digest(item.get('feature')) or item.get('model_sha256') != model_sha:
                raise ValueError('clean_cache_corrupt')
            value = vector(item['feature']); reused += 1
        else:
            if infer is None:
                infer, actual_sha = worker_factory('minilm')
                if actual_sha != model_sha:
                    raise ValueError('raw_clean_model_mismatch')
            value = normalize_inference(infer(actual_input)); checkpoint(); check_resources(); computed += 1
            atomic_json(path, {'fingerprint': fingerprint, 'feature': value.tolist(), 'feature_digest': digest(value.tolist()),
                               'model_sha256': model_sha, 'parser_version': PARSER_VERSION, 'version': VERSION})
        values[i] = value; hashes[str(path.relative_to(output))] = file_digest(path); check_resources()
    summary = {'computed': computed, 'cache_hits': reused, 'unchanged_raw_reused': unchanged, 'model_loads': int(infer is not None),
               'wall_seconds': time.monotonic()-started, 'cpu_threads': 1, 'batch_size': 1, 'version': VERSION, 'model_sha256': model_sha}
    return values, summary, hashes
