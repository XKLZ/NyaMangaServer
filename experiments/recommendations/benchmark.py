"""3000-item synthetic retrieval resource check; no private data or inference."""
import json
import time
from core import atomic_json, checkpoint
from models import offline_environment


def run(output):
    offline_environment()
    import numpy as np
    from features import rss_bytes, check_resources, worker_lock
    with worker_lock(output):
        rng = np.random.default_rng(1729)
        results = {}
        for dimension in (512, 384):
            vectors = rng.standard_normal((3000, dimension), dtype=np.float32)
            vectors /= np.linalg.norm(vectors, axis=1, keepdims=True)
            vectors[1] = vectors[0]
            started = time.monotonic()
            planted_found = False
            for offset in range(0, 3000, 32):
                checkpoint()
                scores = vectors[offset:offset + 32] @ vectors.T
                for j in range(len(scores)):
                    scores[j, offset + j] = -np.inf
                top = np.argpartition(scores, -5, axis=1)[:, -5:]
                if offset == 0:
                    planted_found = 1 in top[0] and 0 in top[1]
            check_resources()
            if not planted_found:
                raise ValueError("synthetic_exact_neighbor_not_retrieved")
            results[str(dimension)] = {"items": 3000, "dimensions": dimension, "top_k": 5, "query_chunk": 32,
                "retrieval_seconds": time.monotonic() - started, "vector_bytes": vectors.nbytes,
                "peak_rss_bytes": rss_bytes(), "planted_exact_neighbor_found": bool(planted_found)}
            del vectors
        report = {"schema": "synthetic-retrieval-v1", "seed": 1729, "synthetic_features_only": True,
                  "includes_model_inference": False, "full_library_recall_claim": False, "nas_verified": False,
                  "cpu_threads": 1, "results": results}
        atomic_json(output / "synthetic-retrieval.json", report)
        print(json.dumps(report))
