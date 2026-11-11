# Local Work and Series recommendation runtime

Production pilot boundary for V4. This directory is independent of `experiments`;
its runtime modules never import experimental commands or read experiment outputs.

The Go service owns this Python process and exchanges bounded JSON-lines messages.
Only configured local model/cache paths are accepted at process start. Requests
carry image bytes and canonical metadata, never a user-supplied server file path.
Inference is CPU-only, single-threaded and offline. Model hashes, preprocessor
versions and Python dependency versions are fixed. No download command is provided.

`worker.py` supports capability checks, per-Edition feature preparation, and a
same-library Edition-to-Work recommendation. Feature vectors travel as little-endian
float32/base64 to keep the whole 2823-Edition pool below the 32MiB frame budget.
Cached vectors are validated without renormalizing them. Private content-addressed
cache writes are atomic; an interrupted write cannot become an active cache entry.

Pinned policy: `sscd_union_fixed_v2`, supplementary threshold 0.65, strong threshold
0.75, with V4 content-risk routing and fixed V2 auxiliary parameters. Scores are
matching signals, not probabilities. Recommendations and preset labels do not
write organization or manual labels. All acceptance is an explicit service action.

SSCD upstream weights/source are MIT; MiniLM is Apache-2.0. Hashes, revisions and
license provenance are in `models.lock.json`. Install the versions in
`requirements.txt` only in an explicitly managed Python environment; the pilot
reuses the already-installed local environment and verified model files.

Anonymous unit checks:

```sh
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python -m unittest discover -s runtime/recommendations -p 'test_runtime.py'
```

Application integration and isolated pilot usage are tracked in
[the pilot document](../../docs/work-recommendation-pilot.md).

Series discovery uses actual Work partitions and all Edition title views. Its
separate `series_*` modules port the frozen V2 constrained average linkage and
representative title profile: strong .70, supplementary .45, cross-pair floor .40,
ambiguity gap .05. Only `series_features` requests MiniLM; it does not prepare cover
vectors. `series_prepare` / `series_row` / `series_cluster` / `series_validate`
exchange checkpointable bounded messages. Repeated organization-only changes reuse
the same Corpus; changed inputs replace its bounded method cache. No experimental
module is imported by these operations. The isolated subprocess parity check lives
in `test_series.py`, not in the production import graph.

Series proposals never mutate organization here. The Go service owns revisions,
durable decisions, acceptance and Series-only Undo. See [Series pilot evidence and
limits](../../docs/series-recommendation-pilot.md); pair metrics are not whole-group
quality guarantees. Targeted anonymous checks:

```sh
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python -B -m unittest discover -s runtime/recommendations -p 'test_series.py'
```

The runtime-only recursive dependency lock for the current Python 3.12.0/macOS ARM64
environment is `requirements-lock-macos-arm64.txt` (23 packages, wheel SHA256 pins).
The capability probe checks installed versions against this closure. Reproduction
on the matching platform uses:

```sh
python3.12 -m venv /explicit/private/runtime
/explicit/private/runtime/bin/python -m pip install --require-hashes -r runtime/recommendations/requirements-lock-macos-arm64.txt
```

This is documentation for an explicitly requested environment installation, not a
startup action. No packages were installed or upgraded during this pilot. Linux AMD64 and ARM64 now have separate same-version locks (23 packages each).
All 46 selected wheels were downloaded from official PyPI release URLs and their
actual bytes/SHA256 and ELF DT_NEEDED entries checked; no packages were installed.
`linux-runtime-verification.json` records filename, release URL, digest, size and
native dependencies. Python stays exactly 3.12.0; macOS lock and model artifacts
are unchanged. Capability metadata selects the explicit platform lock and rejects
unknown platforms/Python versions. These records are not Linux execution or model
inference evidence.

The Python image index is pinned to
`sha256:19a6235339a74eca01227b03629f63b6f5020abc21142436eced6ec3a9839a76`.
Registry queries timed out; the digest/platforms were instead verified against
[Docker official immutable repo-info](https://github.com/docker-library/repo-info/blob/3dae0ec5c55c6a52a67d7349e72bf6687d27a44d/repos/python/remote/3.12.0-slim-bookworm.md),
whose actual metadata SHA256 is pinned by the verifier. No image was pulled or
executed, and current tag freshness is not asserted. Reproduce official checks
with the explicitly authorized `scripts/check/lock-linux-runtime.py
--verify-downloads`; `--image-only --image-metadata-file FILE` accepts an earlier
exact official download only if its pinned SHA256 matches.

ELF inspection found external libc/loader, libdl, libm, libpthread, librt,
libgcc_s, libstdc++ and libz dependencies on both Linux architectures. The fixed
Debian bookworm Python image supplies its glibc/loader family; the Docker runtime
copies libgcc_s/libstdc++/libz (plus libgomp) from the existing pinned target-arch
Go bookworm stage. Build-time package imports and `pip check` fail if dependencies
are missing; those Docker RUN checks have not been executed here. No apt/source
build, floating Python upgrade or startup installation is used.

The Go adapter converts cumulative worker counters to per-call deltas while holding
process ownership. Task accounting persists these deltas, including repeated work
following interruption. `peak_rss_bytes` is the worker's observed lifetime high-water
mark, not a sum and not an isolated per-job peak measurement.


`import_cache.py` is a separate explicit offline snapshot preparation tool, never
called by the service worker. It validates the authorized manifest, actual imported
cover/title bytes, exact model/preprocessor identities and the original cache
feature digest before copying canonical feature records. It holds the pilot
instance lock and never calls inference or downloads anything. Results and counts
are private; missing entries await user-triggered tasks. Run via `make
pilot-reuse-cache` only after the empty-target validation import and while that
isolated instance is stopped.
