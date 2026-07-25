// Command pairbam retains complete primary mate groups or read names shared by two BAMs.
// It replaces the legacy samtools/Picard filtering workflow while preserving explicit mode semantics.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	bamnative "github.com/rainoffallingstar/pairbam/bamnative"
)

var Version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	for _, argument := range arguments {
		switch argument {
		case "--version", "-V":
			fmt.Printf("pairbam %s\n", Version)
			return nil
		case "--help", "-h":
			printUsage()
			return nil
		}
	}

	coordinateSort := false
	positionalArguments := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		switch argument {
		case "--coord-sort":
			coordinateSort = true
		default:
			if strings.HasPrefix(argument, "-") {
				return fmt.Errorf("unknown flag %q", argument)
			}
			positionalArguments = append(positionalArguments, argument)
		}
	}

	switch len(positionalArguments) {
	case 2:
		if !strings.HasSuffix(positionalArguments[0], ".bam") {
			return fmt.Errorf("single BAM input must end with .bam")
		}
		return runSingleBAMMode(positionalArguments[0], positionalArguments[1], coordinateSort)
	case 3:
		return runDualBAMMode(positionalArguments[0], positionalArguments[1], positionalArguments[2], coordinateSort)
	default:
		printUsage()
		return fmt.Errorf("expected two arguments for single mode or three arguments for dual mode")
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  Single BAM mode:  pairbam [--coord-sort] <input.bam> <output.bam>")
	fmt.Println("  Dual BAM mode:    pairbam [--coord-sort] <R1.bam> <R2.bam> <output_prefix>")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --coord-sort    Coordinate-sort the output BAM and create a BAI index.")
	fmt.Println("                  Default (without this flag): name-sort the output, no index.")
	fmt.Println()
	fmt.Println("Single BAM mode:")
	fmt.Println("  Retains read names with exactly one primary mapped R1 and one primary mapped R2 record.")
	fmt.Println("  This is a complete-primary-mates contract, not a SAM FlagProperPair assertion.")
	fmt.Println()
	fmt.Println("Dual BAM mode:")
	fmt.Println("  1. Externally name-sorts both BAM files")
	fmt.Println("  2. Streams unique primary mapped read names present in BOTH files")
	fmt.Println("  3. Writes matched primary records to separate R1 and R2 outputs")
	fmt.Println("  4. Sorts and optionally indexes the output BAM files")
	fmt.Println()
	fmt.Println("Output files (single mode, without --coord-sort):")
	fmt.Println("  <output.bam>     - Complete primary mate groups (name-sorted)")
	fmt.Println()
	fmt.Println("Output files (single mode, with --coord-sort):")
	fmt.Println("  <output.bam>     - Complete primary mate groups (coordinate-sorted)")
	fmt.Println("  <output.bam>.bai - BAI index")
	fmt.Println()
	fmt.Println("Output files (dual mode, without --coord-sort):")
	fmt.Println("  <output_prefix>_R1.bam  - Filtered R1 BAM (name-sorted)")
	fmt.Println("  <output_prefix>_R2.bam  - Filtered R2 BAM (name-sorted)")
	fmt.Println("  <output_prefix>_filtered_readnames.txt")
	fmt.Println()
	fmt.Println("Output files (dual mode, with --coord-sort):")
	fmt.Println("  <output_prefix>_R1.bam     - Filtered R1 BAM (coordinate-sorted)")
	fmt.Println("  <output_prefix>_R1.bam.bai - BAI index for R1")
	fmt.Println("  <output_prefix>_R2.bam     - Filtered R2 BAM (coordinate-sorted)")
	fmt.Println("  <output_prefix>_R2.bam.bai - BAI index for R2")
	fmt.Println("  <output_prefix>_filtered_readnames.txt")
}

// runSingleBAMMode processes a single merged paired-end BAM file
// and filters out incomplete or ambiguous primary mate groups.
func runSingleBAMMode(bamPath, outputPath string, coordinateSort bool) error {
	readnamesPath := strings.TrimSuffix(outputPath, ".bam") + "_filtered_readnames.txt"
	pathRoles := []filePathRole{
		{label: "input BAM", path: bamPath},
		{label: "output BAM", path: outputPath},
		{label: "output BAM index", path: outputPath + ".bai"},
		{label: "filtered read names", path: readnamesPath},
	}
	if err := validateDistinctFilePaths(pathRoles); err != nil {
		return err
	}

	fmt.Printf("Processing single merged paired-end BAM file:\n")
	fmt.Printf("  Input: %s\n", bamPath)
	fmt.Printf("  Output: %s\n", outputPath)

	temporaryDirectory, err := os.MkdirTemp("", "pairbam-single-*")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)

	fmt.Println("\n[1/3] Name-sorting input and analyzing read pair status...")
	nameSortedPath, err := prepareNameSortedInput(bamPath, temporaryDirectory, "input")
	if err != nil {
		return err
	}

	rawStagedPath, err := createStagedArtifactPath(outputPath, ".raw.bam")
	if err != nil {
		return err
	}
	defer os.Remove(rawStagedPath)
	finalStagedPath, err := createStagedArtifactPath(outputPath, ".bam")
	if err != nil {
		return err
	}
	defer os.Remove(finalStagedPath)
	defer os.Remove(finalStagedPath + ".bai")
	readnamesStagedPath, err := createStagedArtifactPath(readnamesPath, ".txt")
	if err != nil {
		return err
	}
	defer os.Remove(readnamesStagedPath)

	streamResult, err := streamSingleNameGroups(nameSortedPath, rawStagedPath, readnamesStagedPath)
	if err != nil {
		return fmt.Errorf("stream single-BAM groups: %w", err)
	}
	fmt.Printf("  Total records: %d\n", streamResult.totalRecords)
	fmt.Printf("  Unique read names: %d\n", streamResult.uniqueReadNames)
	fmt.Printf("  Complete primary pairs: %d\n", streamResult.completePairNames)
	fmt.Printf("  Incomplete or ambiguous read names: %d\n", streamResult.filteredReadNames)

	fmt.Println("\n[2/3] Finalizing retained primary mate groups...")
	fmt.Printf("  Kept %d out of %d records\n", streamResult.keptPrimaryRecords, streamResult.totalRecords)

	if coordinateSort {
		fmt.Println("\n[3/3] Coordinate-sorting and indexing...")
	} else {
		fmt.Println("\n[3/3] Name-sorting output...")
	}
	if err := sortBAM(rawStagedPath, finalStagedPath, coordinateSort); err != nil {
		return err
	}
	if coordinateSort {
		if err := bamnative.BuildIndex(finalStagedPath); err != nil {
			return fmt.Errorf("build staged BAM index: %w", err)
		}
	}

	artifacts := []stagedArtifact{
		{stagedPath: finalStagedPath, targetPath: outputPath},
		{stagedPath: readnamesStagedPath, targetPath: readnamesPath},
	}
	if coordinateSort {
		artifacts = append(artifacts, stagedArtifact{stagedPath: finalStagedPath + ".bai", targetPath: outputPath + ".bai"})
	} else {
		artifacts = append(artifacts, stagedArtifact{targetPath: outputPath + ".bai", removeTarget: true})
	}
	if err := publishArtifacts(artifacts); err != nil {
		return fmt.Errorf("publish single-BAM outputs: %w", err)
	}

	fmt.Println("\nDone!")
	fmt.Printf("\nSummary:\n")
	fmt.Printf("  Total input records: %d\n", streamResult.totalRecords)
	fmt.Printf("  Unique read names: %d\n", streamResult.uniqueReadNames)
	fmt.Printf("  Complete primary pairs (kept): %d\n", streamResult.completePairNames)
	fmt.Printf("  Incomplete or ambiguous read names (filtered): %d\n", streamResult.filteredReadNames)
	fmt.Printf("  Output records: %d\n", streamResult.keptPrimaryRecords)
	fmt.Printf("\nOutput files:\n")
	fmt.Printf("  %s\n", outputPath)
	if coordinateSort {
		fmt.Printf("  %s.bai\n", outputPath)
	}
	fmt.Printf("  %s\n", readnamesPath)
	return nil
}

// runDualBAMMode processes two separate R1/R2 BAM files, keeping only reads
// present in both, then sorts (and optionally indexes) the outputs.
func runDualBAMMode(r1Path, r2Path, outputPrefix string, coordinateSort bool) error {
	outputR1 := outputPrefix + "_R1.bam"
	outputR2 := outputPrefix + "_R2.bam"
	readnamesPath := outputPrefix + "_filtered_readnames.txt"
	pathRoles := []filePathRole{
		{label: "R1 input BAM", path: r1Path},
		{label: "R2 input BAM", path: r2Path},
		{label: "R1 output BAM", path: outputR1},
		{label: "R2 output BAM", path: outputR2},
		{label: "R1 output BAM index", path: outputR1 + ".bai"},
		{label: "R2 output BAM index", path: outputR2 + ".bai"},
		{label: "filtered read names", path: readnamesPath},
	}
	if err := validateDistinctFilePaths(pathRoles); err != nil {
		return err
	}

	fmt.Printf("Processing paired-end BAM files:\n")
	fmt.Printf("  R1: %s\n", r1Path)
	fmt.Printf("  R2: %s\n", r2Path)
	fmt.Printf("  Output prefix: %s\n", outputPrefix)

	temporaryDirectory, err := os.MkdirTemp("", "pairbam-dual-*")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)

	fmt.Println("\n[1/6] Name-sorting R1 input...")
	r1NameSortedPath, err := prepareNameSortedInput(r1Path, temporaryDirectory, "r1")
	if err != nil {
		return err
	}
	fmt.Println("\n[2/6] Name-sorting R2 input...")
	r2NameSortedPath, err := prepareNameSortedInput(r2Path, temporaryDirectory, "r2")
	if err != nil {
		return err
	}
	fmt.Println("\n[3/6] Streaming matched read names...")

	rawR1Path, err := createStagedArtifactPath(outputR1, ".raw.bam")
	if err != nil {
		return err
	}
	defer os.Remove(rawR1Path)
	rawR2Path, err := createStagedArtifactPath(outputR2, ".raw.bam")
	if err != nil {
		return err
	}
	defer os.Remove(rawR2Path)
	finalR1Path, err := createStagedArtifactPath(outputR1, ".bam")
	if err != nil {
		return err
	}
	defer os.Remove(finalR1Path)
	defer os.Remove(finalR1Path + ".bai")
	finalR2Path, err := createStagedArtifactPath(outputR2, ".bam")
	if err != nil {
		return err
	}
	defer os.Remove(finalR2Path)
	defer os.Remove(finalR2Path + ".bai")
	readnamesStagedPath, err := createStagedArtifactPath(readnamesPath, ".txt")
	if err != nil {
		return err
	}
	defer os.Remove(readnamesStagedPath)

	streamResult, err := streamDualNameGroups(
		r1NameSortedPath,
		r2NameSortedPath,
		rawR1Path,
		rawR2Path,
		readnamesStagedPath,
	)
	if err != nil {
		return fmt.Errorf("stream dual-BAM groups: %w", err)
	}
	fmt.Printf("  Found %d unique primary names in R1\n", streamResult.r1ReadNames)
	fmt.Printf("  Found %d unique primary names in R2\n", streamResult.r2ReadNames)
	fmt.Printf("  Found %d matched read names\n", streamResult.matchedReadNames)
	fmt.Printf("  Found %d unmatched read names\n", streamResult.unmatchedReadNames)

	fmt.Println("\n[4/6] Finalized matched R1 records.")
	fmt.Println("\n[5/6] Finalized matched R2 records.")

	if coordinateSort {
		fmt.Println("\n[6/6] Coordinate-sorting and indexing output BAM files...")
	} else {
		fmt.Println("\n[6/6] Name-sorting output BAM files...")
	}
	if err := sortBAM(rawR1Path, finalR1Path, coordinateSort); err != nil {
		return fmt.Errorf("prepare R1 output: %w", err)
	}
	if err := sortBAM(rawR2Path, finalR2Path, coordinateSort); err != nil {
		return fmt.Errorf("prepare R2 output: %w", err)
	}
	if coordinateSort {
		if err := bamnative.BuildIndex(finalR1Path); err != nil {
			return fmt.Errorf("build staged R1 index: %w", err)
		}
		if err := bamnative.BuildIndex(finalR2Path); err != nil {
			return fmt.Errorf("build staged R2 index: %w", err)
		}
	}

	artifacts := []stagedArtifact{
		{stagedPath: finalR1Path, targetPath: outputR1},
		{stagedPath: finalR2Path, targetPath: outputR2},
		{stagedPath: readnamesStagedPath, targetPath: readnamesPath},
	}
	if coordinateSort {
		artifacts = append(
			artifacts,
			stagedArtifact{stagedPath: finalR1Path + ".bai", targetPath: outputR1 + ".bai"},
			stagedArtifact{stagedPath: finalR2Path + ".bai", targetPath: outputR2 + ".bai"},
		)
	} else {
		artifacts = append(
			artifacts,
			stagedArtifact{targetPath: outputR1 + ".bai", removeTarget: true},
			stagedArtifact{targetPath: outputR2 + ".bai", removeTarget: true},
		)
	}
	if err := publishArtifacts(artifacts); err != nil {
		return fmt.Errorf("publish dual-BAM outputs: %w", err)
	}

	fmt.Println("\nDone!")
	fmt.Printf("\nSummary:\n")
	fmt.Printf("  R1 unique primary names: %d\n", streamResult.r1ReadNames)
	fmt.Printf("  R2 unique primary names: %d\n", streamResult.r2ReadNames)
	fmt.Printf("  Matched read names (kept): %d\n", streamResult.matchedReadNames)
	fmt.Printf("  Unmatched read names (filtered out): %d\n", streamResult.unmatchedReadNames)
	fmt.Printf("\nOutput files:\n")
	fmt.Printf("  %s\n", outputR1)
	if coordinateSort {
		fmt.Printf("  %s.bai\n", outputR1)
	}
	fmt.Printf("  %s\n", outputR2)
	if coordinateSort {
		fmt.Printf("  %s.bai\n", outputR2)
	}
	fmt.Printf("  %s\n", readnamesPath)
	return nil
}

// readPairStatus records primary mate multiplicity for one fragment name.
type readPairStatus struct {
	firstMateCount  int
	secondMateCount int
	invalidPrimary  bool
}

func isPrimaryMappedRecord(record *bamnative.Record) bool {
	if record == nil || record.RefID < 0 || record.Flags&bamnative.FlagUnmapped != 0 {
		return false
	}
	return record.Flags&(bamnative.FlagSecondary|bamnative.FlagSupplementary) == 0
}

func updateReadPairStatus(status *readPairStatus, record *bamnative.Record) {
	if !isPrimaryMappedRecord(record) {
		return
	}
	if record.Flags&bamnative.FlagPaired == 0 {
		status.invalidPrimary = true
		return
	}
	isFirstMate := record.Flags&bamnative.FlagFirstInPair != 0
	isSecondMate := record.Flags&bamnative.FlagSecondInPair != 0
	if isFirstMate == isSecondMate {
		status.invalidPrimary = true
		return
	}
	if isFirstMate {
		status.firstMateCount++
		return
	}
	status.secondMateCount++
}

// saveReadNamesList saves a slice of read names to a file (one per line, sorted).
func saveReadNamesList(names []string, path string) error {
	outputFile, err := os.Create(path)
	if err != nil {
		return err
	}

	sortedNames := append([]string(nil), names...)
	sort.Strings(sortedNames)
	writer := bufio.NewWriter(outputFile)
	for _, name := range sortedNames {
		if _, err := writer.WriteString(name + "\n"); err != nil {
			_ = outputFile.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = outputFile.Close()
		return err
	}
	if err := outputFile.Close(); err != nil {
		return err
	}
	return nil
}

// sortBAM sorts inputPath into outputPath without replacing either path.
func sortBAM(inputPath, outputPath string, coordinateSort bool) error {
	if err := bamnative.Sort(inputPath, &bamnative.SortOptions{
		OutputPath:         outputPath,
		ByName:             !coordinateSort,
		MemoryLimitBytes:   pairbamSortMemoryLimitBytes,
		TemporaryDirectory: filepath.Dir(outputPath),
	}); err != nil {
		if coordinateSort {
			return fmt.Errorf("coordinate-sort BAM: %w", err)
		}
		return fmt.Errorf("name-sort BAM: %w", err)
	}
	return nil
}
