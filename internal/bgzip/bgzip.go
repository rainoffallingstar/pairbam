package bgzip

import (
	"io"

	driver "github.com/rainoffallingstar/bamdriver/pkg/bgzip"
)

var (
	ErrHeader      = driver.ErrHeader
	ErrTruncated   = driver.ErrTruncated
	ErrExtraField  = driver.ErrExtraField
	ErrBlockSize   = driver.ErrBlockSize
	ErrNoBGZF      = driver.ErrNoBGZF
	ErrBadChecksum = driver.ErrBadChecksum
)

const (
	BGZF_ID1            = driver.BGZF_ID1
	BGZF_ID2            = driver.BGZF_ID2
	BGZF_CM_DEFLATE     = driver.BGZF_CM_DEFLATE
	BGZF_FLG_FEXTRA     = driver.BGZF_FLG_FEXTRA
	BGZF_XLEN           = driver.BGZF_XLEN
	BGZF_SI1            = driver.BGZF_SI1
	BGZF_SI2            = driver.BGZF_SI2
	BGZF_SLEN           = driver.BGZF_SLEN
	BGZF_MAX_BLOCK_SIZE = driver.BGZF_MAX_BLOCK_SIZE
)

type Block = driver.Block
type Reader = driver.Reader
type Writer = driver.Writer

func NewReader(r io.Reader) (*Reader, error) { return driver.NewReader(r) }
func ReadAll(r io.Reader) ([]byte, error)    { return driver.ReadAll(r) }
func ReadFile(path string) ([]byte, error)   { return driver.ReadFile(path) }
func NewWriter(path string) (*Writer, error) { return driver.NewWriter(path) }
