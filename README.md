# pairbam

**A pure-Go paired-end BAM filter that preserves the read-name groups a downstream workflow needs.**

`pairbam` consumes either one merged BAM or two separate BAMs, selects eligible primary records, and writes filtered BAM outputs. It includes native BGZF support and can publish coordinate-sorted/indexed outputs without samtools or Picard.

## Input modes

### Single merged BAM

Retains complete unique primary R1/R2 mate groups from one input:

```bash
pairbam [--coord-sort] input.bam output.bam
```

### Two BAMs

Retains primary mapped read names present in both inputs:

```bash
pairbam [--coord-sort] R1.bam R2.bam output_prefix
```

A shared read name means name intersection. It does not, by itself, prove SAM `FlagProperPair` or mate-coordinate correctness.

## Output behavior

Without `--coord-sort`, outputs are query-name sorted and no BAI index is written. With `--coord-sort`, outputs are coordinate sorted and indexed.

For dual input with prefix `filtered`, the output family is:

```text
filtered_R1.bam
filtered_R1.bam.bai
filtered_R2.bam
filtered_R2.bam.bai
filtered_filtered_readnames.txt
```

The filtered-name file records names that were not shared between the two inputs. Publication is staged transactionally so partial BAM/BAI output is not presented as a completed result.

## Gate B reproducibility check

The repository includes a fixture-based mate-integrity workflow at `.github/workflows/gate-b.yml`. It runs four cells against one immutable revision of `fallingstar10/otter-data`, verifying each BAM against the published `provenance/checksums.sha256` manifest, running complete-primary-mate filtering with `--coord-sort`, and validating the output with Samtools supplied by `enva`:

| Cell | Fixture | Input | Fragments | Output |
|---|---|---|---|---|
| `rna-pdx` | RNA-PDX graft `SRR30880970` (hg38) | 51,140 records | 20,000 | 40,000 records |
| `bs-pdx-graft` | BS-PDX Note 4 50% cell, Bismark hg19 graft | 525,922 records | 262,961 | 525,922 records |
| `bs-pdx-host` | BS-PDX Note 4 50% cell, Bismark mm10 host | 208,812 records | 104,406 | 208,812 records |
| `bs-pdx-incomplete` | the same graft, with 7 seeded-random read names reduced to one record | 525,915 records | 262,954 | 525,908 records |

The acceptance contract is deliberately narrow: exactly one primary R1 and one primary R2 record per retained read name, no secondary/supplementary/unmapped retained records, no read name rejected as incomplete or ambiguous, and the exact input/output record counts above. The RNA-PDX input carries supplementary alignments, so it also exercises the drop-non-primary path; both BS-PDX cells are clean all-primary inputs, so they assert exact pass-through of a bisulfite-aligned graft and host BAM. This is fragment/mate preservation evidence; it is not a claim that read-name intersection alone proves SAM `proper pair`. Reports, logs, BAM/BAI outputs, SAM snapshots, and tool-version evidence are uploaded per cell as an Actions artifact, including when a preceding step fails.

### Incomplete-mate negative control

The three fixtures above are clean, so `filtered_readnames.txt` is empty for every one of them and the rejection path is never exercised. `scripts/gate-b/make-incomplete-fixture.py` closes that gap: it derives a perturbed copy of the BS-PDX graft in which a seeded random selection of read-name groups keeps an odd number of records — its first record in the fixture's own shuffled record order — instead of the full mate pair. Taking an odd number of reads per name is exactly what makes a fragment incomplete, and because the selection is seeded the perturbation is reproducible.

The workflow then asserts the negative control end to end: the perturbation is real (exactly 7 names carry an odd record count and the record total drops by 7), `pairbam` rejects precisely those 7 names and no others, the filtered list is sorted, and the retained 262,954 fragments still satisfy the full positive contract. A cell that silently stopped perturbing the fixture, or a `pairbam` change that stopped reporting incomplete names, fails the job.

## Install

```bash
git clone https://github.com/rainoffallingstar/pairbam.git
cd pairbam
go build -o pairbam ./cmd/pairbam
```

`pairbam` uses the shared [`bamdriver`](https://github.com/rainoffallingstar/bamdriver) module for low-level BAM/BGZF operations.

## Example

```bash
pairbam --coord-sort input_R1.bam input_R2.bam filtered
```

The tool writes only eligible primary records. Secondary, supplementary, and unmapped records are excluded from eligibility decisions.

## Resource model

- External sorting uses bounded memory and temporary disk runs.
- Single-input processing keeps only the current read-name group.
- Dual-input processing keeps one group per input while streaming the name intersection.
- BAM, BAI, and filtered-name files are staged before publication.

## Development

```bash
gofmt -w .
go test ./...
go vet ./...
```

## License and repository

MIT · [rainoffallingstar/pairbam](https://github.com/rainoffallingstar/pairbam)
