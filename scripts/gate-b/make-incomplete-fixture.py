#!/usr/bin/env python3
"""Manufacture an incomplete-mate negative control from a Gate B fixture.

Gate B asserts that `pairbam` retains complete primary mate groups and reports every read name
it rejects. The published acceptance fixtures are all clean, so `filtered_readnames.txt` is
always empty and the rejection path is never exercised by the positive cells. This script
derives a perturbed copy of a fixture in which a seeded random selection of read-name groups
keeps an odd number of records (its first record in stream order) instead of the full mate
pair, so each selected name is an incomplete fragment.

The perturbation is deliberately expressible as "take the first odd number of reads": for every
selected name the kept record count is the odd number 1, and the record that is kept is the
first one encountered in the fixture's own (already shuffled) record order.

The emitted manifest records the exact expected counts and the selected names, so the workflow
can assert both that the perturbation is real and that `pairbam` rejected precisely those names.

usage:
  make-incomplete-fixture.py \
    --input-bam pristine.bam --output-bam perturbed.bam --manifest perturbed.json \
    --selected-names selected-names.txt --samtools samtools \
    --incomplete-fragments 7 --seed 20260914
"""

from __future__ import annotations

import argparse
import json
import random
import shutil
import subprocess
from pathlib import Path


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input-bam", required=True, type=Path)
    parser.add_argument("--output-bam", required=True, type=Path)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument(
        "--selected-names",
        required=True,
        type=Path,
        help="sorted list of the perturbed read names, for comparison with filtered_readnames.txt",
    )
    parser.add_argument(
        "--samtools",
        required=True,
        help="samtools executable path or a command name resolved on PATH",
    )
    parser.add_argument(
        "--incomplete-fragments",
        required=True,
        type=int,
        help="number of read-name groups to reduce to a single record",
    )
    parser.add_argument("--seed", required=True, type=int, help="seeding for the random selection")
    return parser.parse_args()


def resolve_executable(candidate: str) -> str:
    resolved = shutil.which(candidate)
    if resolved is None:
        raise ValueError(f"samtools is not available: {candidate}")
    return resolved


def stream_sam(samtools: str, bam_path: Path, with_header: bool) -> subprocess.Popen:
    command = [samtools, "view"]
    if with_header:
        command.append("-h")
    command.append(str(bam_path))
    return subprocess.Popen(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
    )


def read_record_names(samtools: str, bam_path: Path) -> dict[str, int]:
    process = stream_sam(samtools, bam_path, with_header=False)
    assert process.stdout is not None
    record_counts: dict[str, int] = {}
    for line in process.stdout:
        name = line.split("\t", 1)[0]
        record_counts[name] = record_counts.get(name, 0) + 1
    process.stdout.close()
    return_code = process.wait()
    if return_code != 0:
        stderr = process.stderr.read() if process.stderr is not None else ""
        raise SystemExit(f"samtools view failed for {bam_path}: {stderr.strip()}")
    if not record_counts:
        raise SystemExit(f"no records found in {bam_path}")
    return record_counts


def main() -> int:
    arguments = parse_arguments()
    if arguments.incomplete_fragments <= 0:
        raise ValueError("--incomplete-fragments must be positive")

    samtools = resolve_executable(arguments.samtools)
    record_counts = read_record_names(samtools, arguments.input_bam)
    multiple_record_names = sorted(
        name for name, count in record_counts.items() if count != 2
    )
    if multiple_record_names:
        # A fixture that is not uniformly diploid makes "drop exactly one record" ambiguous.
        raise SystemExit(
            "fixtures for this perturbation must hold exactly two records per read name; "
            f"found {len(multiple_record_names)} names with a different count, "
            f"for example {multiple_record_names[:3]}"
        )
    if arguments.incomplete_fragments >= len(record_counts):
        raise ValueError("--incomplete-fragments must be smaller than the read-name count")

    generator = random.Random(arguments.seed)
    selected_names = set(
        generator.sample(sorted(record_counts), arguments.incomplete_fragments)
    )

    arguments.output_bam.parent.mkdir(parents=True, exist_ok=True)
    writer = subprocess.Popen(
        [samtools, "view", "-b", "-o", str(arguments.output_bam), "-"],
        stdin=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
    )
    assert writer.stdin is not None
    reader = stream_sam(samtools, arguments.input_bam, with_header=True)
    assert reader.stdout is not None

    seen_selected_names: set[str] = set()
    input_record_count = 0
    output_record_count = 0
    try:
        for line in reader.stdout:
            if line.startswith("@"):
                writer.stdin.write(line)
                continue
            input_record_count += 1
            name = line.split("\t", 1)[0]
            if name in selected_names:
                if name in seen_selected_names:
                    # Keep the odd count of one record per selected name.
                    continue
                seen_selected_names.add(name)
            writer.stdin.write(line)
            output_record_count += 1
    finally:
        reader.stdout.close()
        reader_return_code = reader.wait()
        writer.stdin.close()
        writer_return_code = writer.wait()

    if reader_return_code != 0:
        stderr = reader.stderr.read() if reader.stderr is not None else ""
        raise SystemExit(f"samtools view failed for {arguments.input_bam}: {stderr.strip()}")
    if writer_return_code != 0:
        stderr = writer.stderr.read() if writer.stderr is not None else ""
        raise SystemExit(f"samtools view failed writing {arguments.output_bam}: {stderr.strip()}")
    if seen_selected_names != selected_names:
        missing = sorted(selected_names - seen_selected_names)
        raise SystemExit(f"selected read names were not present in the stream: {missing}")

    selected_names_path = arguments.selected_names
    selected_names_path.parent.mkdir(parents=True, exist_ok=True)
    selected_names_path.write_text(
        "".join(f"{name}\n" for name in sorted(selected_names)), encoding="utf-8"
    )

    manifest = {
        "schema_version": "pairbam.gate-b-incomplete-fixture/v1",
        "samtools": samtools,
        "seed": arguments.seed,
        "incomplete_fragments": arguments.incomplete_fragments,
        "selection": "first record in stream order retained, mate dropped",
        "input_bam": str(arguments.input_bam),
        "output_bam": str(arguments.output_bam),
        "input_records": input_record_count,
        "output_records": output_record_count,
        "input_read_names": len(record_counts),
        "output_read_names": len(record_counts),
        "incomplete_read_names": sorted(selected_names),
    }
    arguments.manifest.parent.mkdir(parents=True, exist_ok=True)
    arguments.manifest.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(
        f"perturbed {arguments.output_bam}: {input_record_count} -> {output_record_count} records, "
        f"{arguments.incomplete_fragments} incomplete read names out of {len(record_counts)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
