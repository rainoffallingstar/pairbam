package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	bamnative "github.com/rainoffallingstar/pairbam/bamnative"
)

const pairbamSortMemoryLimitBytes = int64(64 << 20)

type primaryNameGroup struct {
	name    string
	records []*bamnative.Record
}

type primaryNameGroupReader struct {
	reader       *bamnative.Reader
	pending      *bamnative.Record
	totalRecords int
}

type singleStreamResult struct {
	totalRecords       int
	uniqueReadNames    int
	completePairNames  int
	filteredReadNames  int
	keptPrimaryRecords int
}

type dualStreamResult struct {
	r1ReadNames        int
	r2ReadNames        int
	matchedReadNames   int
	unmatchedReadNames int
}

func prepareNameSortedInput(inputPath, temporaryDirectory, label string) (string, error) {
	outputPath := filepath.Join(temporaryDirectory, label+".queryname.bam")
	if err := bamnative.Sort(inputPath, &bamnative.SortOptions{
		OutputPath:         outputPath,
		ByName:             true,
		MemoryLimitBytes:   pairbamSortMemoryLimitBytes,
		TemporaryDirectory: temporaryDirectory,
	}); err != nil {
		return "", fmt.Errorf("name-sort %s BAM: %w", label, err)
	}
	return outputPath, nil
}

func streamSingleNameGroups(nameSortedPath, rawOutputPath, filteredNamesPath string) (singleStreamResult, error) {
	inputFile, reader, err := openPairbamBAMReader(nameSortedPath)
	if err != nil {
		return singleStreamResult{}, err
	}
	defer inputFile.Close()

	outputWriter, err := bamnative.NewWriter(rawOutputPath, reader.Header())
	if err != nil {
		return singleStreamResult{}, fmt.Errorf("create staged BAM writer: %w", err)
	}
	outputWriterClosed := false
	defer func() {
		if !outputWriterClosed {
			_ = outputWriter.Close()
		}
	}()

	filteredNamesFile, err := os.Create(filteredNamesPath)
	if err != nil {
		return singleStreamResult{}, fmt.Errorf("create staged filtered-name file: %w", err)
	}
	filteredNamesClosed := false
	defer func() {
		if !filteredNamesClosed {
			_ = filteredNamesFile.Close()
		}
	}()
	filteredNamesWriter := bufio.NewWriter(filteredNamesFile)

	groupReader := &primaryNameGroupReader{reader: reader}
	result := singleStreamResult{}
	for {
		group, readErr := groupReader.next()
		if readErr != nil {
			return result, fmt.Errorf("read name-sorted BAM group: %w", readErr)
		}
		if group == nil {
			break
		}
		result.uniqueReadNames++
		status := readPairStatus{}
		for _, record := range group.records {
			updateReadPairStatus(&status, record)
		}
		isCompletePair := !status.invalidPrimary && status.firstMateCount == 1 && status.secondMateCount == 1
		if !isCompletePair {
			if _, err := filteredNamesWriter.WriteString(group.name + "\n"); err != nil {
				return result, fmt.Errorf("write filtered read name: %w", err)
			}
			result.filteredReadNames++
			continue
		}
		result.completePairNames++
		for _, record := range group.records {
			if err := outputWriter.Write(record); err != nil {
				return result, fmt.Errorf("write retained BAM record: %w", err)
			}
			result.keptPrimaryRecords++
		}
	}
	result.totalRecords = groupReader.totalRecords
	if result.totalRecords == 0 {
		return result, fmt.Errorf("no records found in BAM file")
	}
	if err := filteredNamesWriter.Flush(); err != nil {
		return result, fmt.Errorf("flush filtered read names: %w", err)
	}
	if err := filteredNamesFile.Close(); err != nil {
		return result, fmt.Errorf("close filtered read names: %w", err)
	}
	filteredNamesClosed = true
	if err := outputWriter.Close(); err != nil {
		return result, fmt.Errorf("close staged BAM writer: %w", err)
	}
	outputWriterClosed = true
	return result, nil
}

func streamDualNameGroups(
	r1NameSortedPath,
	r2NameSortedPath,
	rawR1OutputPath,
	rawR2OutputPath,
	filteredNamesPath string,
) (dualStreamResult, error) {
	r1File, r1Reader, err := openPairbamBAMReader(r1NameSortedPath)
	if err != nil {
		return dualStreamResult{}, fmt.Errorf("open name-sorted R1 BAM: %w", err)
	}
	defer r1File.Close()
	r2File, r2Reader, err := openPairbamBAMReader(r2NameSortedPath)
	if err != nil {
		return dualStreamResult{}, fmt.Errorf("open name-sorted R2 BAM: %w", err)
	}
	defer r2File.Close()

	r1Writer, err := bamnative.NewWriter(rawR1OutputPath, r1Reader.Header())
	if err != nil {
		return dualStreamResult{}, fmt.Errorf("create staged R1 writer: %w", err)
	}
	r1WriterClosed := false
	defer func() {
		if !r1WriterClosed {
			_ = r1Writer.Close()
		}
	}()
	r2Writer, err := bamnative.NewWriter(rawR2OutputPath, r2Reader.Header())
	if err != nil {
		return dualStreamResult{}, fmt.Errorf("create staged R2 writer: %w", err)
	}
	r2WriterClosed := false
	defer func() {
		if !r2WriterClosed {
			_ = r2Writer.Close()
		}
	}()

	filteredNamesFile, err := os.Create(filteredNamesPath)
	if err != nil {
		return dualStreamResult{}, fmt.Errorf("create staged filtered-name file: %w", err)
	}
	filteredNamesClosed := false
	defer func() {
		if !filteredNamesClosed {
			_ = filteredNamesFile.Close()
		}
	}()
	filteredNamesWriter := bufio.NewWriter(filteredNamesFile)

	r1Groups := &primaryNameGroupReader{reader: r1Reader}
	r2Groups := &primaryNameGroupReader{reader: r2Reader}
	r1Group, err := nextPrimaryGroup(r1Groups)
	if err != nil {
		return dualStreamResult{}, err
	}
	r2Group, err := nextPrimaryGroup(r2Groups)
	if err != nil {
		return dualStreamResult{}, err
	}
	result := dualStreamResult{}

	for r1Group != nil || r2Group != nil {
		switch {
		case r2Group == nil || (r1Group != nil && r1Group.name < r2Group.name):
			result.r1ReadNames++
			result.unmatchedReadNames++
			if _, err := filteredNamesWriter.WriteString(r1Group.name + "\n"); err != nil {
				return result, fmt.Errorf("write unmatched R1 name: %w", err)
			}
			r1Group, err = nextPrimaryGroup(r1Groups)
		case r1Group == nil || r2Group.name < r1Group.name:
			result.r2ReadNames++
			result.unmatchedReadNames++
			if _, err := filteredNamesWriter.WriteString(r2Group.name + "\n"); err != nil {
				return result, fmt.Errorf("write unmatched R2 name: %w", err)
			}
			r2Group, err = nextPrimaryGroup(r2Groups)
		default:
			result.r1ReadNames++
			result.r2ReadNames++
			if len(r1Group.records) != 1 || len(r2Group.records) != 1 {
				return result, fmt.Errorf("read %q has multiple primary alignments", r1Group.name)
			}
			if err := r1Writer.Write(r1Group.records[0]); err != nil {
				return result, fmt.Errorf("write matched R1 record: %w", err)
			}
			if err := r2Writer.Write(r2Group.records[0]); err != nil {
				return result, fmt.Errorf("write matched R2 record: %w", err)
			}
			result.matchedReadNames++
			r1Group, err = nextPrimaryGroup(r1Groups)
			if err == nil {
				r2Group, err = nextPrimaryGroup(r2Groups)
			}
		}
		if err != nil {
			return result, err
		}
	}
	if result.r1ReadNames == 0 {
		return result, fmt.Errorf("no primary mapped reads found in R1 BAM")
	}
	if result.r2ReadNames == 0 {
		return result, fmt.Errorf("no primary mapped reads found in R2 BAM")
	}
	if err := filteredNamesWriter.Flush(); err != nil {
		return result, fmt.Errorf("flush filtered read names: %w", err)
	}
	if err := filteredNamesFile.Close(); err != nil {
		return result, fmt.Errorf("close filtered read names: %w", err)
	}
	filteredNamesClosed = true
	if err := r1Writer.Close(); err != nil {
		return result, fmt.Errorf("close staged R1 writer: %w", err)
	}
	r1WriterClosed = true
	if err := r2Writer.Close(); err != nil {
		return result, fmt.Errorf("close staged R2 writer: %w", err)
	}
	r2WriterClosed = true
	return result, nil
}

func openPairbamBAMReader(path string) (*os.File, *bamnative.Reader, error) {
	inputFile, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	reader, err := bamnative.NewReader(inputFile)
	if err != nil {
		_ = inputFile.Close()
		return nil, nil, err
	}
	return inputFile, reader, nil
}

func nextPrimaryGroup(groupReader *primaryNameGroupReader) (*primaryNameGroup, error) {
	for {
		group, err := groupReader.next()
		if err != nil || group == nil {
			return group, err
		}
		if len(group.records) > 1 {
			return nil, fmt.Errorf("read %q has multiple primary alignments", group.name)
		}
		if len(group.records) == 1 {
			return group, nil
		}
	}
}

func (groupReader *primaryNameGroupReader) next() (*primaryNameGroup, error) {
	firstRecord := groupReader.pending
	groupReader.pending = nil
	if firstRecord == nil {
		var err error
		firstRecord, err = groupReader.reader.Read()
		if err != nil {
			if err == io.EOF {
				return nil, nil
			}
			return nil, err
		}
		groupReader.totalRecords++
	}

	groupName := firstRecord.Name
	primaryRecords := make([]*bamnative.Record, 0, 2)
	if isPrimaryMappedRecord(firstRecord) {
		primaryRecords = append(primaryRecords, firstRecord)
	}
	for {
		record, err := groupReader.reader.Read()
		if err != nil {
			if err == io.EOF {
				return &primaryNameGroup{name: groupName, records: primaryRecords}, nil
			}
			return nil, err
		}
		groupReader.totalRecords++
		if record.Name != groupName {
			groupReader.pending = record
			return &primaryNameGroup{name: groupName, records: primaryRecords}, nil
		}
		if isPrimaryMappedRecord(record) {
			primaryRecords = append(primaryRecords, record)
		}
	}
}
