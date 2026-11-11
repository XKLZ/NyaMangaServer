"""Explicit anonymous public download and offline SSCD conversion validation."""
from __future__ import annotations
import json
import os
from pathlib import Path
import shutil
import subprocess
import time
import tempfile
import platform
import resource
import contextlib
from core import REPO, atomic_json, checkpoint, file_digest, reject_symlinks

MODEL_ROOT = REPO / ".runtime/cache/models/recommendations"
SSCD_REVISION = "95902662f2217a5f4aa45f2a3fc70a01dfd3b66a"
MINILM_REVISION = "e8f8c211226b894fcb81acc59f3b34ba3efd5f42"
MINILM_BASE = "https://huggingface.co/sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2/resolve/" + MINILM_REVISION + "/"
REGISTRY = [
    {"key": "sscd_disc_mixup.torchscript.pt", "url": "https://dl.fbaipublicfiles.com/sscd-copy-detection/sscd_disc_mixup.torchscript.pt", "expected_sha256": "9f26bd4c848cc19b73d2ae92eea6e04886f61a7b764ceb7a13aeee62e6a6db56", "digest_origin": "local_pin_first_official_download", "license": "MIT (official repository)", "source_code_revision": SSCD_REVISION, "weight_version": "unversioned_static_official_url_pinned_by_sha256", "max_bytes": 150_000_000},
    {"key": "sscd-LICENSE", "url": "https://raw.githubusercontent.com/facebookresearch/sscd-copy-detection/" + SSCD_REVISION + "/LICENSE", "expected_sha256": None, "license": "MIT", "revision": SSCD_REVISION, "max_bytes": 100_000},
    {"key": "minilm/model.onnx", "url": MINILM_BASE + "onnx/model.onnx", "expected_sha256": "10f7a088420252b26caf819236ca2c9d2987afd0fc06fec7553b542a5655a05a", "license": "Apache-2.0 (official model card)", "revision": MINILM_REVISION, "max_bytes": 471_000_000},
    {"key": "minilm/tokenizer.json", "url": MINILM_BASE + "tokenizer.json", "expected_sha256": "2c3387be76557bd40970cec13153b3bbf80407865484b209e655e5e4729076b8", "license": "Apache-2.0", "revision": MINILM_REVISION, "max_bytes": 10_000_000},
]
for name in ("README.md", "config.json", "tokenizer_config.json", "special_tokens_map.json", "sentence_bert_config.json", "1_Pooling/config.json"):
    REGISTRY.append({"key": "minilm/" + name, "url": MINILM_BASE + name, "expected_sha256": None, "license": "Apache-2.0", "revision": MINILM_REVISION, "max_bytes": 100_000})
lock_path = Path(__file__).with_name("model-artifacts.lock.json")
reject_symlinks(lock_path)
locked_files = json.loads(lock_path.read_text())["files"]
for item in REGISTRY:
    locked = locked_files[item["key"]]
    if item["expected_sha256"] and item["expected_sha256"] != locked["sha256"]:
        raise ValueError("source_and_model_lock_disagree")
    item["expected_sha256"] = locked["sha256"]
    item["digest_origin"] = locked["digest_provenance"]


def offline_environment():
    # No hub clients are used; these also guard against later implicit downloads.
    for key, value in {"HF_HUB_OFFLINE": "1", "TRANSFORMERS_OFFLINE": "1", "HF_HUB_DISABLE_IMPLICIT_TOKEN": "1", "HF_HUB_DISABLE_TELEMETRY": "1", "DO_NOT_TRACK": "1", "TOKENIZERS_PARALLELISM": "false", "OMP_NUM_THREADS": "1", "OPENBLAS_NUM_THREADS": "1", "MKL_NUM_THREADS": "1", "VECLIB_MAXIMUM_THREADS": "1", "NUMEXPR_NUM_THREADS": "1"}.items():
        os.environ[key] = value


def fetch():
    """Only this command can download; never accepts a private query or arbitrary URL."""
    reject_symlinks(MODEL_ROOT)
    MODEL_ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(MODEL_ROOT, 0o700)
    if shutil.disk_usage(MODEL_ROOT).free < 3 * 1024**3:
        raise ValueError("insufficient_model_disk_budget")
    manifest_path = MODEL_ROOT / "manifest.json"
    reject_symlinks(manifest_path)
    previous = json.loads(manifest_path.read_text()) if manifest_path.exists() else {"files": {}}
    manifest = {"schema": "model-artifacts-v1", "files": {}}
    for item in REGISTRY:
        checkpoint()
        target = MODEL_ROOT / item["key"]
        reject_symlinks(target)
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(target.parent, 0o700)
        old = previous["files"].get(item["key"], {})
        if target.exists():
            actual = file_digest(target)
            expected = item["expected_sha256"] or old.get("sha256")
            if expected is None or actual != expected:
                raise ValueError("unverified_existing_model_artifact:" + item["key"])
        else:
            temporary = target.with_suffix(target.suffix + ".download")
            if temporary.is_symlink():
                raise ValueError("unsafe_download_target")
            fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
            os.fchmod(fd, 0o600);os.close(fd)
            # -q ignores ~/.curlrc; no auth, tokens, netrc or user configuration.
            process = subprocess.Popen(["/usr/bin/curl", "-q", "--fail", "--silent", "--show-error", "--location", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "20", "--max-time", "900", "--max-filesize", str(item["max_bytes"]), "--output", str(temporary), item["url"]])
            try:
                while process.poll() is None:
                    checkpoint()
                    time.sleep(0.1)
                checkpoint()
            except BaseException:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill();process.wait()
                temporary.unlink(missing_ok=True)
                raise
            if process.returncode:
                raise RuntimeError("official_download_failed:" + item["key"])
            os.chmod(temporary, 0o600)
            actual = file_digest(temporary)
            if item["expected_sha256"] and actual != item["expected_sha256"]:
                raise ValueError("official_lfs_digest_mismatch:" + item["key"])
            os.replace(temporary, target)
        manifest["files"][item["key"]] = {**item, "sha256": actual, "bytes": target.stat().st_size,
            "digest_provenance": item.get("digest_origin") or ("publisher_huggingface_lfs_metadata" if item["expected_sha256"] else "local_download_fingerprint_not_publisher_signature")}
        atomic_json(manifest_path, manifest)
        print(json.dumps({"artifact": item["key"], "bytes": target.stat().st_size, "sha256": actual}), flush=True)
    return manifest


def artifact(key):
    reject_symlinks(MODEL_ROOT / key)
    reject_symlinks(MODEL_ROOT / "manifest.json")
    manifest = json.loads((MODEL_ROOT / "manifest.json").read_text())
    row = manifest["files"][key]
    path = MODEL_ROOT / key
    if file_digest(path) != row["sha256"]:
        raise ValueError("model_artifact_fingerprint_mismatch")
    return path, row["sha256"]


@contextlib.contextmanager
def temporary_model(target):
    reject_symlinks(target)
    fd, temp_name = tempfile.mkstemp(prefix=".sscd-conversion-", suffix=".onnx", dir=target.parent)
    os.close(fd)
    temporary = Path(temp_name)
    try:
        yield temporary
    finally:
        temporary.unlink(missing_ok=True)


def convert_sscd():
    offline_environment()
    import numpy as np
    import onnx
    import onnxruntime as ort
    import torch
    torch.set_num_threads(1)
    torch.set_num_interop_threads(1)
    source, sha = artifact("sscd_disc_mixup.torchscript.pt")
    target = MODEL_ROOT / "sscd_disc_mixup-skew320-opset17.onnx"
    reject_symlinks(target)
    with temporary_model(target) as temporary:
        model = torch.jit.load(str(source), map_location="cpu").eval()
        started = time.monotonic()
        generator = torch.Generator().manual_seed(1729)
        # Anonymous nonuniform image-like input exercises activations; not private data.
        x = torch.rand((1, 3, 320, 320), generator=generator)
        with torch.inference_mode():
            torch.onnx.export(model, x, str(temporary), input_names=["image"], output_names=["embedding"], opset_version=17, do_constant_folding=True, dynamo=False)
        onnx.checker.check_model(str(temporary))
        options = ort.SessionOptions();options.intra_op_num_threads = 1;options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        session = ort.InferenceSession(str(temporary), sess_options=options, providers=["CPUExecutionProvider"])
        errors = []
        for value in (x, torch.zeros_like(x), torch.ones_like(x)):
            with torch.inference_mode():
                reference = model(value).numpy()
            actual = session.run(None, {"image": value.numpy()})[0]
            if actual.shape != (1, 512) or not np.isfinite(actual).all():
                raise ValueError("sscd_conversion_invalid_output")
            errors.append(float(np.max(np.abs(reference - actual))))
        if max(errors) > 2e-4:
            temporary.unlink(missing_ok=True)
            raise ValueError("sscd_conversion_reference_mismatch")
        result = {"schema": "sscd-conversion-v1", "source_sha256": sha, "onnx_sha256": file_digest(temporary), "opset": 17,
                  "input_shape": [1, 3, 320, 320], "dimensions": 512, "preprocessing": "PIL-RGB-bilinear-skew320-imagenet-meanstd-v1",
                  "reference_max_absolute_errors": errors, "tolerance": 2e-4, "passed": True,
                  "torch_version": torch.__version__, "onnx_version": onnx.__version__, "onnxruntime_version": ort.__version__,
                  "duration_seconds": time.monotonic() - started, "nas_compatibility_verified": False,
                  "conversion_peak_rss_bytes": int(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * (1 if platform.system() == "Darwin" else 1024)),
                  "conversion_is_reference_only_not_deployment_worker": True}
        os.replace(temporary, target)
        atomic_json(MODEL_ROOT / "sscd-conversion.json", result)
        print(json.dumps(result))
