package bamnative

import (
	"io"

	driver "github.com/rainoffallingstar/bamdriver/pkg/bamnative"
)

var (
	ErrInvalidHeader     = driver.ErrInvalidHeader
	ErrInvalidRecord     = driver.ErrInvalidRecord
	ErrMissingField      = driver.ErrMissingField
	ErrInvalidCigar      = driver.ErrInvalidCigar
	ErrInvalidAux        = driver.ErrInvalidAux
	ErrNoRecords         = driver.ErrNoRecords
	ErrReferenceNotFound = driver.ErrReferenceNotFound
)

type Header = driver.Header
type Reference = driver.Reference
type Record = driver.Record
type CigarOp = driver.CigarOp
type AuxField = driver.AuxField
type Reader = driver.Reader

type Writer = driver.Writer
type WriterAt = driver.WriterAt
type SortOptions = driver.SortOptions

const (
	CigarMatch     = driver.CigarMatch
	CigarInsertion = driver.CigarInsertion
	CigarDeletion  = driver.CigarDeletion
	CigarSkip      = driver.CigarSkip
	CigarSoftClip  = driver.CigarSoftClip
	CigarHardClip  = driver.CigarHardClip
	CigarPadding   = driver.CigarPadding
	CigarEqual     = driver.CigarEqual
	CigarMismatch  = driver.CigarMismatch
)

const (
	FlagPaired        = driver.FlagPaired
	FlagProperPair    = driver.FlagProperPair
	FlagUnmapped      = driver.FlagUnmapped
	FlagMateUnmapped  = driver.FlagMateUnmapped
	FlagReverse       = driver.FlagReverse
	FlagMateReverse   = driver.FlagMateReverse
	FlagFirstInPair   = driver.FlagFirstInPair
	FlagSecondInPair  = driver.FlagSecondInPair
	FlagSecondary     = driver.FlagSecondary
	FlagQCFail        = driver.FlagQCFail
	FlagDuplicate     = driver.FlagDuplicate
	FlagSupplementary = driver.FlagSupplementary
)

const (
	AuxTypeChar   = driver.AuxTypeChar
	AuxTypeInt8   = driver.AuxTypeInt8
	AuxTypeUInt8  = driver.AuxTypeUInt8
	AuxTypeInt16  = driver.AuxTypeInt16
	AuxTypeUInt16 = driver.AuxTypeUInt16
	AuxTypeInt32  = driver.AuxTypeInt32
	AuxTypeUInt32 = driver.AuxTypeUInt32
	AuxTypeFloat  = driver.AuxTypeFloat
	AuxTypeDouble = driver.AuxTypeDouble
	AuxTypeString = driver.AuxTypeString
	AuxTypeHex    = driver.AuxTypeHex
	AuxTypeArray  = driver.AuxTypeArray
)

func NewReader(r io.Reader) (*Reader, error) { return driver.NewReader(r) }
func NewWriter(path string, header *Header) (*Writer, error) {
	return driver.NewWriter(path, header)
}
func HasIndex(bamPath string) bool                   { return driver.HasIndex(bamPath) }
func BuildIndex(bamPath string) error                { return driver.BuildIndex(bamPath) }
func EnsureIndex(bamPath string) error               { return driver.EnsureIndex(bamPath) }
func Sort(inputPath string, opts *SortOptions) error { return driver.Sort(inputPath, opts) }
func IsSorted(path string) (bool, error)             { return driver.IsSorted(path) }
func SortAndIndexIfNeeded(inputPath, outputPath string) error {
	return driver.SortAndIndexIfNeeded(inputPath, outputPath)
}
