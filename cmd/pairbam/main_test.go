package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bamnative "github.com/rainoffallingstar/pairbam/bamnative"
)

func TestPrintUsage_NoError(t *testing.T) {
	printUsage()
}

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
}

func TestUpdateReadPairStatusRejectsInvalidMateFlags(t *testing.T) {
	status := readPairStatus{}
	updateReadPairStatus(&status, &bamnative.Record{
		Name:  "invalid",
		RefID: 0,
		Flags: bamnative.FlagPaired | bamnative.FlagFirstInPair | bamnative.FlagSecondInPair,
	})
	if !status.invalidPrimary {
		t.Fatal("record marked as both R1 and R2 must be invalid")
	}
}

func TestRunAcceptsHelpAndRejectsUnknownFlag(t *testing.T) {
	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("help returned an error: %v", err)
	}
	if err := run([]string{"--unknown"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unknown flag error = %v, want explicit rejection", err)
	}
}

func TestRunSingleBAMModeRejectsSameInputOutputWithoutChangingSource(t *testing.T) {
	bamPath := filepath.Join(t.TempDir(), "input.bam")
	originalContent := []byte("original BAM bytes")
	if err := os.WriteFile(bamPath, originalContent, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	err := runSingleBAMMode(bamPath, bamPath, false)
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("same-path error = %v, want identity rejection", err)
	}
	currentContent, readErr := os.ReadFile(bamPath)
	if readErr != nil {
		t.Fatalf("read source after rejection: %v", readErr)
	}
	if string(currentContent) != string(originalContent) {
		t.Fatalf("source changed after same-path rejection: %q", currentContent)
	}
}

func TestRunSingleBAMModeRejectsIndexAliasWithoutChangingSource(t *testing.T) {
	for aliasType, createAlias := range map[string]func(string, string) error{
		"direct path": func(sourcePath, aliasPath string) error {
			return os.Rename(sourcePath, aliasPath)
		},
		"symlink": func(sourcePath, aliasPath string) error {
			return os.Symlink(sourcePath, aliasPath)
		},
		"hardlink": func(sourcePath, aliasPath string) error {
			return os.Link(sourcePath, aliasPath)
		},
	} {
		t.Run(aliasType, func(t *testing.T) {
			rootDirectory := t.TempDir()
			outputPath := filepath.Join(rootDirectory, "output.bam")
			indexPath := outputPath + ".bai"
			inputPath := filepath.Join(rootDirectory, "input.bam")
			originalContent := []byte("protected input BAM bytes")
			if err := os.WriteFile(inputPath, originalContent, 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}
			if err := createAlias(inputPath, indexPath); err != nil {
				t.Fatalf("create %s alias: %v", aliasType, err)
			}
			if aliasType == "direct path" {
				inputPath = indexPath
			}

			err := runSingleBAMMode(inputPath, outputPath, false)
			if err == nil || !strings.Contains(err.Error(), "same file") {
				t.Fatalf("index alias error = %v, want identity rejection", err)
			}
			currentContent, readErr := os.ReadFile(inputPath)
			if readErr != nil {
				t.Fatalf("read protected input after rejection: %v", readErr)
			}
			if string(currentContent) != string(originalContent) {
				t.Fatalf("protected input changed after rejection: %q", currentContent)
			}
		})
	}
}

func TestRunDualBAMModeRejectsInputAtRemovedIndexPath(t *testing.T) {
	rootDirectory := t.TempDir()
	outputPrefix := filepath.Join(rootDirectory, "paired")
	r1Path := outputPrefix + "_R1.bam.bai"
	r2Path := filepath.Join(rootDirectory, "r2.bam")
	originalContent := []byte("protected R1 BAM bytes")
	if err := os.WriteFile(r1Path, originalContent, 0o600); err != nil {
		t.Fatalf("write R1 source: %v", err)
	}
	if err := os.WriteFile(r2Path, []byte("R2 BAM bytes"), 0o600); err != nil {
		t.Fatalf("write R2 source: %v", err)
	}

	err := runDualBAMMode(r1Path, r2Path, outputPrefix, false)
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("dual index alias error = %v, want identity rejection", err)
	}
	currentContent, readErr := os.ReadFile(r1Path)
	if readErr != nil {
		t.Fatalf("read protected R1 after rejection: %v", readErr)
	}
	if string(currentContent) != string(originalContent) {
		t.Fatalf("protected R1 changed after rejection: %q", currentContent)
	}
}

func TestValidateDistinctFilePathsRejectsSymlinkAndHardlinkAliases(t *testing.T) {
	rootDirectory := t.TempDir()
	originalPath := filepath.Join(rootDirectory, "original.bam")
	if err := os.WriteFile(originalPath, []byte("source"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}

	symlinkPath := filepath.Join(rootDirectory, "symlink.bam")
	if err := os.Symlink(originalPath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	hardlinkPath := filepath.Join(rootDirectory, "hardlink.bam")
	if err := os.Link(originalPath, hardlinkPath); err != nil {
		t.Fatalf("create hardlink: %v", err)
	}

	for aliasLabel, aliasPath := range map[string]string{
		"symlink":  symlinkPath,
		"hardlink": hardlinkPath,
	} {
		err := validateDistinctFilePaths([]filePathRole{
			{label: "input", path: originalPath},
			{label: aliasLabel, path: aliasPath},
		})
		if err == nil || !strings.Contains(err.Error(), "same file") {
			t.Fatalf("%s alias error = %v, want identity rejection", aliasLabel, err)
		}
	}
}

func TestPublishArtifactsRestoresPreviousOutputsWhenSecondPublishFails(t *testing.T) {
	rootDirectory := t.TempDir()
	firstTarget := filepath.Join(rootDirectory, "first.bam")
	secondTarget := filepath.Join(rootDirectory, "second.bam")
	firstStaged := filepath.Join(rootDirectory, "first.staged")
	secondStaged := filepath.Join(rootDirectory, "second.staged")
	for path, content := range map[string]string{
		firstTarget:  "old first",
		secondTarget: "old second",
		firstStaged:  "new first",
		secondStaged: "new second",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	originalRenameArtifact := renameArtifact
	renameCount := 0
	renameArtifact = func(oldPath, newPath string) error {
		renameCount++
		if renameCount == 4 {
			return errors.New("injected second publish failure")
		}
		return os.Rename(oldPath, newPath)
	}
	t.Cleanup(func() { renameArtifact = originalRenameArtifact })

	err := publishArtifacts([]stagedArtifact{
		{stagedPath: firstStaged, targetPath: firstTarget},
		{stagedPath: secondStaged, targetPath: secondTarget},
	})
	if err == nil || !strings.Contains(err.Error(), "injected second publish failure") {
		t.Fatalf("publish error = %v, want injected failure", err)
	}
	for path, expectedContent := range map[string]string{
		firstTarget:  "old first",
		secondTarget: "old second",
	} {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read restored output %s: %v", path, readErr)
		}
		if string(content) != expectedContent {
			t.Fatalf("restored output %s = %q, want %q", path, content, expectedContent)
		}
	}
}

func TestPublishArtifactsReportsBackupAndRestoreFailures(t *testing.T) {
	rootDirectory := t.TempDir()
	firstTarget := filepath.Join(rootDirectory, "first.bam")
	secondTarget := filepath.Join(rootDirectory, "second.bam")
	firstStaged := filepath.Join(rootDirectory, "first.staged")
	secondStaged := filepath.Join(rootDirectory, "second.staged")
	for path, content := range map[string]string{
		firstTarget:  "old first",
		secondTarget: "old second",
		firstStaged:  "new first",
		secondStaged: "new second",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	originalRenameArtifact := renameArtifact
	renameCount := 0
	renameArtifact = func(oldPath, newPath string) error {
		renameCount++
		switch renameCount {
		case 2:
			return errors.New("injected backup failure")
		case 3:
			return errors.New("injected restore failure")
		default:
			return os.Rename(oldPath, newPath)
		}
	}
	t.Cleanup(func() { renameArtifact = originalRenameArtifact })

	err := publishArtifacts([]stagedArtifact{
		{stagedPath: firstStaged, targetPath: firstTarget},
		{stagedPath: secondStaged, targetPath: secondTarget},
	})
	if err == nil {
		t.Fatal("publish succeeded despite injected backup and restore failures")
	}
	for _, expectedMessage := range []string{"injected backup failure", "injected restore failure"} {
		if !strings.Contains(err.Error(), expectedMessage) {
			t.Fatalf("publish error = %v, want %q", err, expectedMessage)
		}
	}
}

func TestRunSingleBAMModeStreamsCompletePrimaryMateGroups(t *testing.T) {
	rootDirectory := t.TempDir()
	inputPath := filepath.Join(rootDirectory, "input.bam")
	outputPath := filepath.Join(rootDirectory, "output.bam")
	writePairbamFixtureBAM(t, inputPath, []*bamnative.Record{
		newPairbamFixtureRecord("incomplete", bamnative.FlagPaired|bamnative.FlagFirstInPair, 300),
		newPairbamFixtureRecord("complete", bamnative.FlagPaired|bamnative.FlagSecondInPair, 200),
		newPairbamFixtureRecord("complete", bamnative.FlagPaired|bamnative.FlagFirstInPair, 100),
	})

	if err := runSingleBAMMode(inputPath, outputPath, false); err != nil {
		t.Fatalf("single BAM mode failed: %v", err)
	}
	outputNames := readPairbamFixtureNames(t, outputPath)
	if strings.Join(outputNames, ",") != "complete,complete" {
		t.Fatalf("single output names = %v, want complete pair", outputNames)
	}
	filteredNames, err := os.ReadFile(strings.TrimSuffix(outputPath, ".bam") + "_filtered_readnames.txt")
	if err != nil {
		t.Fatalf("read filtered names: %v", err)
	}
	if string(filteredNames) != "incomplete\n" {
		t.Fatalf("filtered names = %q, want incomplete", filteredNames)
	}
}

func TestRunSingleBAMModeRemovesStaleIndexForNameSortedOutput(t *testing.T) {
	rootDirectory := t.TempDir()
	inputPath := filepath.Join(rootDirectory, "input.bam")
	outputPath := filepath.Join(rootDirectory, "output.bam")
	writePairbamFixtureBAM(t, inputPath, []*bamnative.Record{
		newPairbamFixtureRecord("complete", bamnative.FlagPaired|bamnative.FlagFirstInPair, 100),
		newPairbamFixtureRecord("complete", bamnative.FlagPaired|bamnative.FlagSecondInPair, 200),
	})
	if err := os.WriteFile(outputPath+".bai", []byte("stale index"), 0o600); err != nil {
		t.Fatalf("write stale index: %v", err)
	}

	if err := runSingleBAMMode(inputPath, outputPath, false); err != nil {
		t.Fatalf("name-sorted single mode failed: %v", err)
	}
	if _, err := os.Stat(outputPath + ".bai"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale index still exists or stat failed: %v", err)
	}
}

func TestRunSingleBAMModeSupportsEmptyFilteredOutput(t *testing.T) {
	rootDirectory := t.TempDir()
	inputPath := filepath.Join(rootDirectory, "input.bam")
	outputPath := filepath.Join(rootDirectory, "output.bam")
	writePairbamFixtureBAM(t, inputPath, []*bamnative.Record{
		newPairbamFixtureRecord("incomplete", bamnative.FlagPaired|bamnative.FlagFirstInPair, 100),
	})

	if err := runSingleBAMMode(inputPath, outputPath, false); err != nil {
		t.Fatalf("single BAM empty-output mode failed: %v", err)
	}
	if outputNames := readPairbamFixtureNames(t, outputPath); len(outputNames) != 0 {
		t.Fatalf("empty filtered output contains records: %v", outputNames)
	}
}

func TestRunDualBAMModeStreamsMatchedNamesWithoutClaimingProperPair(t *testing.T) {
	rootDirectory := t.TempDir()
	r1Path := filepath.Join(rootDirectory, "r1.bam")
	r2Path := filepath.Join(rootDirectory, "r2.bam")
	outputPrefix := filepath.Join(rootDirectory, "matched")
	writePairbamFixtureBAM(t, r1Path, []*bamnative.Record{
		newPairbamFixtureRecord("r1-only", 0, 300),
		newPairbamFixtureRecord("shared", 0, 100),
	})
	writePairbamFixtureBAM(t, r2Path, []*bamnative.Record{
		newPairbamFixtureRecord("r2-only", 0, 400),
		newPairbamFixtureRecord("shared", 0, 200),
	})

	if err := runDualBAMMode(r1Path, r2Path, outputPrefix, false); err != nil {
		t.Fatalf("dual BAM mode failed: %v", err)
	}
	if names := readPairbamFixtureNames(t, outputPrefix+"_R1.bam"); strings.Join(names, ",") != "shared" {
		t.Fatalf("R1 matched output names = %v, want shared", names)
	}
	if names := readPairbamFixtureNames(t, outputPrefix+"_R2.bam"); strings.Join(names, ",") != "shared" {
		t.Fatalf("R2 matched output names = %v, want shared", names)
	}
	filteredNames, err := os.ReadFile(outputPrefix + "_filtered_readnames.txt")
	if err != nil {
		t.Fatalf("read dual filtered names: %v", err)
	}
	if string(filteredNames) != "r1-only\nr2-only\n" {
		t.Fatalf("dual filtered names = %q", filteredNames)
	}
}

func TestRunDualBAMModeRejectsDuplicatePrimaryNameOnEitherSide(t *testing.T) {
	rootDirectory := t.TempDir()
	r1Path := filepath.Join(rootDirectory, "r1.bam")
	r2Path := filepath.Join(rootDirectory, "r2.bam")
	writePairbamFixtureBAM(t, r1Path, []*bamnative.Record{
		newPairbamFixtureRecord("duplicate", 0, 100),
		newPairbamFixtureRecord("duplicate", 0, 200),
	})
	writePairbamFixtureBAM(t, r2Path, []*bamnative.Record{
		newPairbamFixtureRecord("other", 0, 300),
	})

	err := runDualBAMMode(r1Path, r2Path, filepath.Join(rootDirectory, "matched"), false)
	if err == nil || !strings.Contains(err.Error(), "multiple primary alignments") {
		t.Fatalf("duplicate primary error = %v, want explicit rejection", err)
	}
}

func writePairbamFixtureBAM(t *testing.T, path string, records []*bamnative.Record) {
	t.Helper()
	header := &bamnative.Header{
		SortOrder:  "coordinate",
		References: []*bamnative.Reference{{ID: 0, Name: "chr1", Len: 1000}},
	}
	writer, err := bamnative.NewWriter(path, header)
	if err != nil {
		t.Fatalf("create fixture BAM: %v", err)
	}
	for _, record := range records {
		if err := writer.Write(record); err != nil {
			_ = writer.Close()
			t.Fatalf("write fixture BAM record: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close fixture BAM: %v", err)
	}
}

func newPairbamFixtureRecord(name string, flags uint16, position int32) *bamnative.Record {
	return &bamnative.Record{
		Name:      name,
		Flags:     flags,
		RefID:     0,
		Pos:       position,
		MapQ:      60,
		Cigar:     []bamnative.CigarOp{{Op: bamnative.CigarMatch, Len: 5}},
		MateRefID: 0,
		MatePos:   position + 50,
		Seq:       "ACGTN",
		Qual:      []byte{30, 30, 30, 30, 30},
	}
}

func readPairbamFixtureNames(t *testing.T, path string) []string {
	t.Helper()
	inputFile, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture BAM %s: %v", path, err)
	}
	defer inputFile.Close()
	reader, err := bamnative.NewReader(inputFile)
	if err != nil {
		t.Fatalf("create fixture BAM reader: %v", err)
	}
	var names []string
	for {
		record, readErr := reader.Read()
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			t.Fatalf("read fixture BAM: %v", readErr)
		}
		names = append(names, record.Name)
	}
	return names
}

func TestWriteLines(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	err := saveReadNamesList([]string{"hello world"}, tmpFile)
	if err != nil {
		t.Fatalf("writeLines failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}

	expected := "hello world\n"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}
