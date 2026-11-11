"""Offline pinned CPU inference, bounded inputs and content-addressed cache."""
import hashlib
import json
import math
import importlib.metadata
import os
from pathlib import Path
import platform
import resource
import time
import warnings

for key in ('OMP_NUM_THREADS', 'OPENBLAS_NUM_THREADS', 'MKL_NUM_THREADS', 'VECLIB_MAXIMUM_THREADS', 'NUMEXPR_NUM_THREADS'):
    os.environ[key] = '1'
for key, value in {'HF_HUB_OFFLINE': '1', 'TRANSFORMERS_OFFLINE': '1', 'HF_HUB_DISABLE_IMPLICIT_TOKEN': '1', 'HF_HUB_DISABLE_TELEMETRY': '1', 'DO_NOT_TRACK': '1', 'TOKENIZERS_PARALLELISM': 'false'}.items():
    os.environ[key] = value

import numpy as np
from common import atomic_json, digest, file_digest, no_symlinks, private_dir

VERSIONS = {'phash': 'phash-dct32-low8-dc-excluded-v1', 'sscd': 'sscd-skew320-bilinear-imagenet-v1', 'minilm': 'minilm-preferred-jpn-raw-title-128-mean-l2-v1'}
RSS_BUDGET = 2 * 1024**3


def rss_bytes():
    value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    return int(value if platform.system() == 'Darwin' else value * 1024)


def check_resources():
    if rss_bytes() > RSS_BUDGET:
        raise MemoryError('recommendation_memory_budget')


def phash(image):
    from PIL import Image
    pixels = np.asarray(image.convert('L').resize((32, 32), Image.Resampling.LANCZOS), dtype=np.float64)
    x = np.arange(32)
    frequencies = np.arange(8)[:, None]
    basis = np.cos(math.pi * (2 * x + 1) * frequencies / 64)
    coefficients = (basis @ pixels @ basis.T).reshape(-1)
    median = np.median(coefficients[1:])
    bits = coefficients > median
    bits[0] = False
    return f'{sum(int(value) << i for i, value in enumerate(bits)):016x}'


def image_from_bytes(raw):
    import io
    from PIL import Image
    if not raw or len(raw) > 10 * 1024**2:
        raise ValueError('image_byte_limit')
    Image.MAX_IMAGE_PIXELS = 20_000_000
    with warnings.catch_warnings():
        warnings.simplefilter('error', Image.DecompressionBombWarning)
        with Image.open(io.BytesIO(raw)) as image:
            if image.format not in ('JPEG', 'PNG', 'WEBP', 'GIF', 'BMP'):
                raise ValueError('image_format_not_supported')
            if image.width <= 0 or image.height <= 0 or image.width * image.height > 20_000_000:
                raise ValueError('image_pixel_limit')
            image.seek(0)
            image.load()
            return image.convert('RGB')


def vector(value, algorithm):
    result = np.asarray(value, dtype=np.float32)
    size = 512 if algorithm == 'sscd' else 384
    if result.shape != (size,) or not np.isfinite(result).all() or not .999 <= float(np.linalg.norm(result)) <= 1.001:
        raise ValueError('invalid_cached_vector')
    # Stored float32 is canonical. Do not normalize a cached vector a second time.
    return result


class Runtime:
    def __init__(self, models, cache):
        self.root = no_symlinks(models)
        self.cache = private_dir(cache)
        self.lock = json.loads(Path(__file__).with_name('models.lock.json').read_text())
        self.loaded = {}
        self.hashes = {}
        self.stats = {'cache_hits': 0, 'computed_inputs': 0, 'model_loads': 0, 'inference_seconds': 0.0}

    def artifact(self, name, expected):
        path = no_symlinks(self.root / name)
        if not path.is_file():
            raise ValueError('model_missing')
        if name not in self.hashes:
            if file_digest(path) != expected:
                raise ValueError('model_checksum_mismatch')
            self.hashes[name] = expected
        return path

    def model_identity(self, algorithm):
        if algorithm == 'phash':
            return 'none'
        if algorithm == 'sscd':
            expected = self.lock['sscd_conversion']['onnx_sha256']
            self.artifact('sscd_disc_mixup-skew320-opset17.onnx', expected)
            return expected
        names = ('minilm/model.onnx', 'minilm/tokenizer.json', 'minilm/sentence_bert_config.json', 'minilm/1_Pooling/config.json')
        hashes = []
        for name in names:
            expected = self.lock['files'][name]['sha256']
            self.artifact(name, expected)
            hashes.append(expected)
        return digest(hashes)

    def capabilities(self):
        dependencies = {}
        runtime_platform = (platform.system(), platform.machine().lower())
        locks = {('Darwin', 'arm64'): 'macos-arm64', ('Linux', 'x86_64'): 'linux-amd64', ('Linux', 'aarch64'): 'linux-arm64'}
        if runtime_platform not in locks:
            raise ValueError('runtime_platform_unsupported')
        if platform.python_version() != '3.12.0':
            raise ValueError('runtime_python_version_mismatch')
        lock = Path(__file__).with_name('requirements-lock-' + locks[runtime_platform] + '.txt')
        if not lock.is_file():
            raise ValueError('runtime_platform_lock_unavailable')
        for declaration in lock.read_text().splitlines():
            if declaration.startswith('#') or not declaration.strip():
                continue
            name, expected = declaration.split()[0].split('==')
            actual = importlib.metadata.version(name)
            if actual != expected:
                raise ValueError('runtime_dependency_version_mismatch')
            dependencies[name] = actual
        models = {algorithm: self.model_identity(algorithm) for algorithm in ('sscd', 'minilm')}
        return {'models': models, 'dependencies': dependencies, 'runtime_platform': locks[runtime_platform], 'python': platform.python_version(), 'available': True, 'algorithm': 'work-cover-recommendation-v4.1', 'feature_versions': VERSIONS, 'cpu_threads': 1, 'rss_budget_bytes': RSS_BUDGET, 'nas_performance_verified': False}

    def session(self, path):
        import onnxruntime as ort
        options = ort.SessionOptions()
        options.intra_op_num_threads = 1
        options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_BASIC
        options.enable_mem_pattern = False
        result = ort.InferenceSession(str(path), sess_options=options, providers=['CPUExecutionProvider'])
        check_resources()
        return result

    def load(self, algorithm):
        if algorithm in self.loaded:
            return self.loaded[algorithm]
        from PIL import Image
        self.model_identity(algorithm)
        if algorithm == 'phash':
            function = phash
        elif algorithm == 'sscd':
            model = self.session(self.root / 'sscd_disc_mixup-skew320-opset17.onnx')
            def function(image):
                array = np.asarray(image.resize((320, 320), Image.Resampling.BILINEAR), dtype=np.float32) / 255
                array = (array - np.asarray([.485, .456, .406], dtype=np.float32)) / np.asarray([.229, .224, .225], dtype=np.float32)
                result = model.run(None, {'image': array.transpose(2, 0, 1)[None].copy()})[0]
                if result.shape != (1, 512):
                    raise ValueError('wrong_sscd_dimensions')
                return self.normalize(result[0])
        else:
            from tokenizers import Tokenizer
            sequence = json.loads((self.root / 'minilm/sentence_bert_config.json').read_text())
            pooling = json.loads((self.root / 'minilm/1_Pooling/config.json').read_text())
            if sequence.get('max_seq_length') != 128 or pooling.get('word_embedding_dimension') != 384 or not pooling.get('pooling_mode_mean_tokens') or any(pooling.get(k) for k in ('pooling_mode_cls_token', 'pooling_mode_max_tokens', 'pooling_mode_mean_sqrt_len_tokens')):
                raise ValueError('unsupported_minilm_preprocessing')
            tokenizer = Tokenizer.from_file(str(self.root / 'minilm/tokenizer.json'))
            tokenizer.enable_truncation(max_length=128)
            model = self.session(self.root / 'minilm/model.onnx')
            names = {v.name for v in model.get_inputs()}
            def function(text):
                encoded = tokenizer.encode(text)
                values = {'input_ids': np.asarray([encoded.ids], dtype=np.int64), 'attention_mask': np.asarray([encoded.attention_mask], dtype=np.int64), 'token_type_ids': np.asarray([encoded.type_ids], dtype=np.int64)}
                result = model.run(None, {k: v for k, v in values.items() if k in names})[0]
                if result.ndim != 3 or result.shape[-1] != 384:
                    raise ValueError('wrong_minilm_dimensions')
                mask = values['attention_mask'][..., None].astype(np.float32)
                pooled = (result * mask).sum(axis=1) / np.maximum(mask.sum(axis=1), 1e-9)
                return self.normalize(pooled[0])
        self.loaded[algorithm] = function
        if algorithm != 'phash':
            self.stats['model_loads'] += 1
        return function

    @staticmethod
    def normalize(value):
        result = np.asarray(value, dtype=np.float32)
        n = float(np.linalg.norm(result))
        if not np.isfinite(result).all() or n < 1e-12:
            raise ValueError('invalid_embedding')
        return (result / n).tolist()

    def cached_feature(self, algorithm, fingerprint, input_sha=None):
        import re
        model_sha=self.model_identity(algorithm)
        if not isinstance(fingerprint,str) or not re.fullmatch('[0-9a-f]{64}',fingerprint):
            return None
        path=no_symlinks(self.cache/algorithm/(fingerprint+'.json'))
        try:
            if not path.is_file() or path.stat().st_size>65536:return None
            item=json.loads(path.read_text())
            sha=item['input_sha256']
            if item.get('fingerprint')!=fingerprint or fingerprint!=digest([VERSIONS[algorithm],model_sha,sha]) or item.get('feature_digest')!=digest(item.get('feature')) or item.get('model_sha256')!=model_sha or item.get('algorithm_version')!=VERSIONS[algorithm] or (input_sha is not None and sha!=input_sha):return None
            if algorithm=='phash':
                if not isinstance(item['feature'],str) or len(item['feature'])!=16:return None
                int(item['feature'],16)
            else:vector(item['feature'],algorithm)
            self.stats['cache_hits']+=1
            return item
        except (OSError,ValueError,KeyError,TypeError):return None

    def feature(self, algorithm, value, repair=False):
        model_sha = self.model_identity(algorithm)
        input_sha = digest(value) if algorithm == 'minilm' else hashlib.sha256(value).hexdigest()
        fingerprint = digest([VERSIONS[algorithm], model_sha, input_sha])
        path = no_symlinks(self.cache / algorithm / (fingerprint + '.json'))
        if repair:
            cached=self.cached_feature(algorithm,fingerprint,input_sha)
            if cached is not None:return cached
        if path.is_file() and not repair:
            if path.stat().st_size > 65536:
                raise ValueError('feature_cache_oversize')
            item = json.loads(path.read_text())
            if item.get('fingerprint') != fingerprint or item.get('feature_digest') != digest(item.get('feature')) or item.get('model_sha256') != model_sha or item.get('input_sha256') != input_sha or item.get('algorithm_version') != VERSIONS[algorithm]:
                raise ValueError('feature_cache_corrupt')
            self.stats['cache_hits'] += 1
        else:
            started = time.monotonic()
            feature = self.load(algorithm)(value if algorithm == 'minilm' else image_from_bytes(value))
            check_resources()
            self.stats['computed_inputs'] += 1
            self.stats['inference_seconds'] += time.monotonic() - started
            item = {'fingerprint': fingerprint, 'input_sha256': input_sha, 'algorithm_version': VERSIONS[algorithm], 'model_sha256': model_sha, 'feature': feature, 'feature_digest': digest(feature)}
            atomic_json(path, item)
        if algorithm == 'phash':
            if not isinstance(item['feature'], str) or len(item['feature']) != 16:
                raise ValueError('invalid_phash')
            int(item['feature'], 16)
        else:
            vector(item['feature'], algorithm)
        return item
