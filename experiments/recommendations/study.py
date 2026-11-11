#!/usr/bin/env python3
"""CLI; default/private inputs and outputs stay below .runtime."""
import argparse
import collections
import json
import time
from core import INPUT, RUNTIME, VERSION, atomic_json, digest, install_signals, private_output, read_source, reject_symlinks, select_pairs, validate_labels


def prepare(output):
    from review import render
    started = time.monotonic()
    books, source = read_source()
    pairs, isolation = select_pairs(books)
    pack = {"schema": VERSION, "source": source, "isolation": isolation, "pairs": pairs}
    pack["pack_id"] = digest(pack)
    atomic_json(output / "candidates.json", pack)
    # Private whitelist export is the sole downstream source; it excludes filepaths/URLs.
    atomic_json(output / "books.json", {"source": source, "books": list(books.values())})
    atomic_json(output / "labels-input.json", {"schema": "recommendation-labels-v1", "pack_id": pack["pack_id"], "annotations": [{"pair_id": p["pair_id"], "label": None, "reason": ""} for p in pairs]})
    images = render(pack, books, INPUT, output / "review.html")
    summary = {"candidate_pairs": len(pairs), "counts": dict(collections.Counter(p["task"] + ":" + p["stratum"] for p in pairs)), "isolation": isolation, "duration_seconds": round(time.monotonic() - started, 3), **images}
    atomic_json(output / "preparation-summary.json", summary)
    print(json.dumps(summary))


def main():
    install_signals()
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["prepare", "prepare-v3", "prepare-v2", "prepare-v2-init", "prepare-v2-features", "prepare-v2-finalize", "validate-labels", "features", "score", "evaluate", "fetch-models", "convert-sscd", "benchmark"])
    parser.add_argument("--output")
    parser.add_argument("--labels")
    parser.add_argument("--algorithm", choices=["phash", "sscd", "minilm"], default="phash")
    parser.add_argument("--limit", type=int, default=0)
    args = parser.parse_args()
    default_run = 'run-v3' if args.command == 'prepare-v3' else ('run-v2' if args.command.startswith('prepare-v2') else 'run-v1')
    output = private_output(args.output or str(RUNTIME / default_run))
    if args.command == 'prepare-v3':
        import sampling_v3
        sampling_v3.prepare(output)
    elif args.command.startswith("prepare-v2"):
        import sampling_v2
        if args.command == "prepare-v2": sampling_v2.prepare(output)
        elif args.command == "prepare-v2-init": sampling_v2.initialize(output)
        elif args.command == "prepare-v2-features": sampling_v2.compute(output, args.algorithm)
        else: sampling_v2.finalize(output)
    elif args.command == "prepare":
        if (output / "candidates.json").exists():
            raise ValueError("candidate_pack_exists_choose_new_output")
        prepare(output)
    elif args.command == "validate-labels":
        from pathlib import Path
        if not args.labels:
            parser.error("validate-labels requires --labels")
        label_path = Path(args.labels).absolute()
        reject_symlinks(label_path)
        if not label_path.resolve().is_relative_to(output) or label_path.stat().st_size > 16 * 1024**2:
            raise ValueError("annotation_input_outside_selected_run_or_too_large")
        pack = json.loads((output / "candidates.json").read_text())
        if pack.get("schema") == "recommendation-pairs-v2":
            from sampling_v2 import validate_labels as validate_v2
            labels = validate_v2(pack, json.loads(label_path.read_text()))
        else:
            labels = validate_labels(pack, json.loads(label_path.read_text()))
        atomic_json(output / "labels-reviewed.json", labels)
        print(json.dumps({"validated_annotations": sum(bool(p.get("label")) for p in labels["annotations"])}))
    elif args.command in ("fetch-models", "convert-sscd"):
        import models
        (models.fetch if args.command == "fetch-models" else models.convert_sscd)()
    elif args.command == "benchmark":
        import benchmark
        benchmark.run(output)
    else:
        if json.loads((output / "candidates.json").read_text()).get("schema") == "recommendation-pairs-v2":
            raise ValueError("v2_preparation_only_analysis_waits_for_user_annotations")
        import features
        {"features": features.run, "score": features.score, "evaluate": features.evaluate}[args.command](output, args)


if __name__ == "__main__":
    try:
        main()
    except InterruptedError:
        print('{"status":"cancelled_resume_with_same_command"}')
        raise SystemExit(130)
