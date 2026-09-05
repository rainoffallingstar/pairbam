// Command paircompare validates that pairbam single-BAM output is an exact
// canonical-record subset selected by the complete-primary-mates contract.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	bamnative "github.com/rainoffallingstar/pairbam/bamnative"
)

type pairValidationReport struct {
	SchemaVersion             string `json:"schema_version"`
	InputPath                 string `json:"input_path"`
	OutputPath                string `json:"output_path"`
	CreatedAtUTC              string `json:"created_at_utc"`
	InputRecords              int64  `json:"input_records"`
	OutputRecords             int64  `json:"output_records"`
	InputFragmentNames        int64  `json:"input_fragment_names"`
	RetainedFragmentNames     int64  `json:"retained_fragment_names"`
	FilteredFragmentNames     int64  `json:"filtered_fragment_names"`
	ExpectedRetainedRecords   int64  `json:"expected_retained_records"`
	ObservedRetainedRecords   int64  `json:"observed_retained_records"`
	HeadersReferenceEqual     bool   `json:"headers_reference_equal"`
	RetainedRecordsEqual      bool   `json:"retained_records_equal"`
	PairRelationshipsEqual    bool   `json:"pair_relationships_equal"`
	FilteredNameManifestEqual bool   `json:"filtered_name_manifest_equal"`
	Equal                     bool   `json:"equal"`
	FirstDifference           string `json:"first_difference,omitempty"`
	InputRetainedDigest       string `json:"input_retained_digest"`
	OutputDigest              string `json:"output_digest"`
}

type primaryPairStatus struct {
	firstMateCount  int
	secondMateCount int
	invalidPrimary  bool
}

type namedGroup struct {
	name    string
	records []*bamnative.Record
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "sort-name" {
		runNameSort(os.Args[2:])
		return
	}

	flagSet := flag.NewFlagSet("paircompare", flag.ExitOnError)
	inputPath := flagSet.String("input", "", "name-sorted input BAM path")
	outputPath := flagSet.String("output", "", "pairbam output BAM path")
	filteredNamesPath := flagSet.String("filtered-names", "", "pairbam filtered-name manifest path")
	reportPath := flagSet.String("report", "", "validation report JSON path")
	flagSet.Parse(os.Args[1:])
	if *inputPath == "" || *outputPath == "" || *filteredNamesPath == "" || *reportPath == "" {
		fmt.Fprintln(os.Stderr, "paircompare requires --input, --output, --filtered-names, and --report")
		os.Exit(2)
	}
	report, err := validatePairbamOutput(*inputPath, *outputPath, *filteredNamesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report.CreatedAtUTC = time.Now().UTC().Format(time.RFC3339)
	if err := writeReport(*reportPath, report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !report.Equal {
		fmt.Fprintln(os.Stderr, report.FirstDifference)
		os.Exit(1)
	}
}

func runNameSort(arguments []string) {
	flagSet := flag.NewFlagSet("sort-name", flag.ExitOnError)
	inputPath := flagSet.String("input", "", "input BAM path")
	outputPath := flagSet.String("output", "", "name-sorted output BAM path")
	temporaryDirectory := flagSet.String("temporary", "", "temporary directory for external sort")
	flagSet.Parse(arguments)
	if *inputPath == "" || *outputPath == "" || *temporaryDirectory == "" {
		fmt.Fprintln(os.Stderr, "sort-name requires --input, --output, and --temporary")
		os.Exit(2)
	}
	if err := bamnative.Sort(*inputPath, &bamnative.SortOptions{
		OutputPath:         *outputPath,
		ByName:             true,
		MemoryLimitBytes:   64 << 20,
		TemporaryDirectory: *temporaryDirectory,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validatePairbamOutput(inputPath string, outputPath string, filteredNamesPath string) (pairValidationReport, error) {
	report := pairValidationReport{
		SchemaVersion:             "gate6.pairbam-integrity/v1",
		InputPath:                 inputPath,
		OutputPath:                outputPath,
		RetainedRecordsEqual:      true,
		PairRelationshipsEqual:    true,
		FilteredNameManifestEqual: true,
	}

	inputFile, inputReader, err := openReader(inputPath)
	if err != nil {
		return report, fmt.Errorf("open input BAM: %w", err)
	}
	defer inputFile.Close()
	outputFile, outputReader, err := openReader(outputPath)
	if err != nil {
		return report, fmt.Errorf("open output BAM: %w", err)
	}
	defer outputFile.Close()
	report.HeadersReferenceEqual = referencesEqual(inputReader.Header(), outputReader.Header())
	if !report.HeadersReferenceEqual {
		report.FirstDifference = "input and output BAM reference declarations differ"
		return report, nil
	}

	filteredNames, err := readFilteredNames(filteredNamesPath)
	if err != nil {
		return report, err
	}
	filteredNameIndex := 0
	inputDigest := sha256.New()
	outputDigest := sha256.New()
	inputGroupReader := &groupReader{reader: inputReader}
	outputGroupReader := &groupReader{reader: outputReader}

	for {
		inputGroup, inputErr := inputGroupReader.next()
		if inputErr != nil {
			return report, fmt.Errorf("read input group: %w", inputErr)
		}
		if inputGroup == nil {
			break
		}
		report.InputFragmentNames++
		report.InputRecords += int64(len(inputGroup.records))
		status := pairStatus(inputGroup.records)
		retained := isCompletePrimaryPair(status)
		if retained {
			report.RetainedFragmentNames++
			report.ExpectedRetainedRecords += 2
			outputGroup, outputErr := outputGroupReader.next()
			if outputErr != nil {
				return report, fmt.Errorf("read output group: %w", outputErr)
			}
			if outputGroup == nil {
				report.RetainedRecordsEqual = false
				setFirstDifference(&report, fmt.Sprintf("retained fragment %q is missing from output", inputGroup.name))
				continue
			}
			report.OutputRecords += int64(len(outputGroup.records))
			if outputGroup.name != inputGroup.name || len(outputGroup.records) != 2 {
				report.RetainedRecordsEqual = false
				setFirstDifference(&report, fmt.Sprintf("retained fragment %q has unexpected output group", inputGroup.name))
				continue
			}
			inputRecordsByMate := recordsByMate(inputGroup.records)
			outputRecordsByMate := recordsByMate(outputGroup.records)
			for mateFlag, inputRecord := range inputRecordsByMate {
				outputRecord, exists := outputRecordsByMate[mateFlag]
				if !exists || canonicalRecord(inputRecord) != canonicalRecord(outputRecord) {
					report.RetainedRecordsEqual = false
					setFirstDifference(&report, fmt.Sprintf("retained record differs for fragment %q mate flag %d", inputGroup.name, mateFlag))
					continue
				}
				writeDigest(inputDigest, inputRecord)
				writeDigest(outputDigest, outputRecord)
			}
			if !pairPointersEqual(inputRecordsByMate, outputRecordsByMate) {
				report.PairRelationshipsEqual = false
				setFirstDifference(&report, fmt.Sprintf("mate relationship differs for fragment %q", inputGroup.name))
			}
			continue
		}

		report.FilteredFragmentNames++
		if filteredNameIndex >= len(filteredNames) || filteredNames[filteredNameIndex] != inputGroup.name {
			report.FilteredNameManifestEqual = false
			setFirstDifference(&report, fmt.Sprintf("filtered-name manifest mismatch at fragment %d", report.FilteredFragmentNames))
		} else {
			filteredNameIndex++
		}
	}
	remainingOutputGroup, outputErr := outputGroupReader.next()
	if outputErr != nil {
		return report, fmt.Errorf("read remaining output group: %w", outputErr)
	}
	if remainingOutputGroup != nil {
		report.RetainedRecordsEqual = false
		report.OutputRecords += int64(len(remainingOutputGroup.records))
		setFirstDifference(&report, fmt.Sprintf("unexpected output fragment %q", remainingOutputGroup.name))
	}
	if filteredNameIndex != len(filteredNames) {
		report.FilteredNameManifestEqual = false
		setFirstDifference(&report, "filtered-name manifest contains names not present in filtered input groups")
	}
	report.ObservedRetainedRecords = report.OutputRecords
	report.InputRetainedDigest = hex.EncodeToString(inputDigest.Sum(nil))
	report.OutputDigest = hex.EncodeToString(outputDigest.Sum(nil))
	report.Equal = report.HeadersReferenceEqual && report.ExpectedRetainedRecords == report.ObservedRetainedRecords && report.RetainedRecordsEqual && report.PairRelationshipsEqual && report.FilteredNameManifestEqual && report.InputRetainedDigest == report.OutputDigest
	if !report.Equal && report.FirstDifference == "" {
		report.FirstDifference = "pairbam subset validation failed"
	}
	return report, nil
}

func openReader(path string) (*os.File, *bamnative.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	reader, err := bamnative.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, reader, nil
}

func (reader *groupReader) next() (*namedGroup, error) {
	firstRecord := reader.pending
	reader.pending = nil
	if firstRecord == nil {
		var err error
		firstRecord, err = reader.reader.Read()
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	group := &namedGroup{name: firstRecord.Name, records: []*bamnative.Record{firstRecord}}
	for {
		record, err := reader.reader.Read()
		if errors.Is(err, io.EOF) {
			return group, nil
		}
		if err != nil {
			return nil, err
		}
		if record.Name != group.name {
			reader.pending = record
			return group, nil
		}
		group.records = append(group.records, record)
	}
}

type groupReader struct {
	reader  *bamnative.Reader
	pending *bamnative.Record
}

func pairStatus(records []*bamnative.Record) primaryPairStatus {
	status := primaryPairStatus{}
	for _, record := range records {
		if !isPrimaryMappedRecord(record) {
			continue
		}
		if record.Flags&bamnative.FlagPaired == 0 {
			status.invalidPrimary = true
			continue
		}
		isFirst := record.Flags&bamnative.FlagFirstInPair != 0
		isSecond := record.Flags&bamnative.FlagSecondInPair != 0
		if isFirst == isSecond {
			status.invalidPrimary = true
			continue
		}
		if isFirst {
			status.firstMateCount++
		} else {
			status.secondMateCount++
		}
	}
	return status
}

func isCompletePrimaryPair(status primaryPairStatus) bool {
	return !status.invalidPrimary && status.firstMateCount == 1 && status.secondMateCount == 1
}

func isPrimaryMappedRecord(record *bamnative.Record) bool {
	return record != nil && record.RefID >= 0 && record.Flags&bamnative.FlagUnmapped == 0 && record.Flags&(bamnative.FlagSecondary|bamnative.FlagSupplementary) == 0
}

func recordsByMate(records []*bamnative.Record) map[uint16]*bamnative.Record {
	result := make(map[uint16]*bamnative.Record, 2)
	for _, record := range records {
		if isPrimaryMappedRecord(record) {
			mateFlags := record.Flags & (bamnative.FlagFirstInPair | bamnative.FlagSecondInPair)
			result[mateFlags] = record
		}
	}
	return result
}

func pairPointersEqual(left map[uint16]*bamnative.Record, right map[uint16]*bamnative.Record) bool {
	for mateFlags, leftRecord := range left {
		rightRecord, exists := right[mateFlags]
		if !exists || leftRecord.MateRefID != rightRecord.MateRefID || leftRecord.MatePos != rightRecord.MatePos || leftRecord.TLen != rightRecord.TLen {
			return false
		}
	}
	return true
}

func referencesEqual(left *bamnative.Header, right *bamnative.Header) bool {
	if len(left.References) != len(right.References) {
		return false
	}
	for referenceIndex := range left.References {
		if left.References[referenceIndex].Name != right.References[referenceIndex].Name || left.References[referenceIndex].Len != right.References[referenceIndex].Len {
			return false
		}
	}
	return true
}

func canonicalRecord(record *bamnative.Record) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%q|%d|%d|%d|%d|", record.Name, record.Flags, record.RefID, record.Pos, record.MapQ)
	for _, operation := range record.Cigar {
		fmt.Fprintf(&builder, "%d%c", operation.Len, operation.Op)
	}
	fmt.Fprintf(&builder, "|%d|%d|%d|%q|%x|", record.MateRefID, record.MatePos, record.TLen, record.Seq, record.Qual)
	for _, auxiliaryField := range record.Aux {
		fmt.Fprintf(&builder, "%s:%c:%c:%v;", auxiliaryField.Tag, auxiliaryField.Type, auxiliaryField.ArrayType, auxiliaryField.Value)
	}
	return builder.String()
}

func writeDigest(digest io.Writer, record *bamnative.Record) {
	_, _ = io.WriteString(digest, canonicalRecord(record))
	_, _ = io.WriteString(digest, "\n")
}

func readFilteredNames(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open filtered-name manifest: %w", err)
	}
	defer file.Close()
	var names []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name := strings.TrimSuffix(scanner.Text(), "\r")
		if name != "" {
			names = append(names, name)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read filtered-name manifest: %w", err)
	}
	return names, nil
}

func setFirstDifference(report *pairValidationReport, message string) {
	if report.FirstDifference == "" {
		report.FirstDifference = message
	}
}

func writeReport(path string, report pairValidationReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(contents, '\n'), 0o644)
}
