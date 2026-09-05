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
