package main

import (
	"os"
	"path/filepath"
	"testing"

	bamnative "github.com/rainoffallingstar/pairbam/bamnative"
)

func TestValidatePairbamOutputAcceptsCompletePrimarySubset(t *testing.T) {
	temporaryDirectory := t.TempDir()
	inputPath := filepath.Join(temporaryDirectory, "input.bam")
	outputPath := filepath.Join(temporaryDirectory, "output.bam")
	filteredNamesPath := filepath.Join(temporaryDirectory, "filtered.txt")

	header := fixtureHeader()
	writeBAM(t, inputPath, header, []*bamnative.Record{
		fixtureRecord("filtered", bamnative.FlagPaired|bamnative.FlagFirstInPair, 10, 20, 40),
		fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagFirstInPair, 30, 50, 40),
		fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagSecondInPair|bamnative.FlagReverse, 50, 30, -40),
	})
	writeBAM(t, outputPath, header, []*bamnative.Record{
		fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagFirstInPair, 30, 50, 40),
		fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagSecondInPair|bamnative.FlagReverse, 50, 30, -40),
	})
	if err := os.WriteFile(filteredNamesPath, []byte("filtered\n"), 0o644); err != nil {
		t.Fatalf("write filtered names: %v", err)
	}

	report, err := validatePairbamOutput(inputPath, outputPath, filteredNamesPath)
	if err != nil {
		t.Fatalf("validatePairbamOutput: %v", err)
	}
	if !report.Equal {
		t.Fatalf("report = %+v, want equality", report)
	}
	if report.ExpectedRetainedRecords != 2 || report.ObservedRetainedRecords != 2 {
		t.Fatalf("record counts = expected %d observed %d, want 2/2", report.ExpectedRetainedRecords, report.ObservedRetainedRecords)
	}
}

func TestValidatePairbamOutputRejectsMateMutation(t *testing.T) {
	temporaryDirectory := t.TempDir()
	inputPath := filepath.Join(temporaryDirectory, "input.bam")
	outputPath := filepath.Join(temporaryDirectory, "output.bam")
	filteredNamesPath := filepath.Join(temporaryDirectory, "filtered.txt")

	header := fixtureHeader()
	inputRecords := []*bamnative.Record{
		fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagFirstInPair, 30, 50, 40),
		fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagSecondInPair|bamnative.FlagReverse, 50, 30, -40),
	}
	mutatedRecord := fixtureRecord("retained", bamnative.FlagPaired|bamnative.FlagSecondInPair|bamnative.FlagReverse, 50, 31, -40)
	writeBAM(t, inputPath, header, inputRecords)
	writeBAM(t, outputPath, header, []*bamnative.Record{inputRecords[0], mutatedRecord})
	if err := os.WriteFile(filteredNamesPath, nil, 0o644); err != nil {
		t.Fatalf("write filtered names: %v", err)
	}

	report, err := validatePairbamOutput(inputPath, outputPath, filteredNamesPath)
	if err != nil {
		t.Fatalf("validatePairbamOutput: %v", err)
	}
	if report.Equal || report.RetainedRecordsEqual || report.PairRelationshipsEqual {
		t.Fatalf("report = %+v, want detected mutation", report)
	}
}

func fixtureHeader() *bamnative.Header {
	return &bamnative.Header{
		Version:   "1.6",
		SortOrder: "queryname",
		References: []*bamnative.Reference{
			{ID: 0, Name: "chr1", Len: 1000},
		},
	}
}

func fixtureRecord(name string, flags uint16, position int32, matePosition int32, templateLength int32) *bamnative.Record {
	return &bamnative.Record{
		Name:      name,
		Flags:     flags,
		RefID:     0,
		Pos:       position,
		MapQ:      60,
		Cigar:     []bamnative.CigarOp{{Op: bamnative.CigarMatch, Len: 4}},
		MateRefID: 0,
		MatePos:   matePosition,
		TLen:      templateLength,
		Seq:       "ACGT",
		Qual:      []byte{30, 30, 30, 30},
		Aux: []*bamnative.AuxField{
			{Tag: "NM", Type: bamnative.AuxTypeInt32, Value: int32(0)},
		},
	}
}

func writeBAM(t *testing.T, path string, header *bamnative.Header, records []*bamnative.Record) {
	t.Helper()
	writer, err := bamnative.NewWriter(path, header)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for recordIndex, record := range records {
		if err := writer.Write(record); err != nil {
			_ = writer.Close()
			t.Fatalf("Write record %d: %v", recordIndex, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
