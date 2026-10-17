package content

import (
	"bytes"
	"errors"
	"io"
)

// Format is the encoding of a page's .rm file, read from its header.
type Format int

const (
	// FormatUnknown means no .rm blob, a short blob, or a header this
	// package does not recognise.
	FormatUnknown Format = iota
	// FormatV3 is header "reMarkable .lines file, version=3          ".
	FormatV3
	// FormatV5 is header "reMarkable .lines file, version=5          ".
	FormatV5
	// FormatV6 is header "reMarkable .lines file, version=6          ".
	FormatV6
)

// HeaderLen is the length of every .rm header: 33 ASCII characters padded
// with spaces to 43 bytes.
const HeaderLen = 43

const headerPrefix = "reMarkable .lines file, version="

func (f Format) String() string {
	switch f {
	case FormatV3:
		return "v3"
	case FormatV5:
		return "v5"
	case FormatV6:
		return "v6"
	default:
		return "unknown"
	}
}

// DetectFormat reads the format from the first HeaderLen bytes of a .rm
// file. Input shorter than HeaderLen yields FormatUnknown. The version digit
// must be followed by a space, so "version=60" is not taken for v6; the rest
// of the padding is not checked.
func DetectFormat(header []byte) Format {
	if len(header) < HeaderLen {
		return FormatUnknown
	}
	header = header[:HeaderLen]
	if !bytes.HasPrefix(header, []byte(headerPrefix)) {
		return FormatUnknown
	}
	if header[len(headerPrefix)+1] != ' ' {
		return FormatUnknown
	}
	switch header[len(headerPrefix)] {
	case '3':
		return FormatV3
	case '5':
		return FormatV5
	case '6':
		return FormatV6
	default:
		return FormatUnknown
	}
}

// DetectFormatReader reads up to HeaderLen bytes from r and detects the
// format. A reader that ends before HeaderLen bytes yields FormatUnknown and
// no error; any other read error is returned.
func DetectFormatReader(r io.Reader) (Format, error) {
	buf := make([]byte, HeaderLen)
	n, err := io.ReadFull(r, buf)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return FormatUnknown, nil
		}
		return FormatUnknown, err
	}
	return DetectFormat(buf[:n]), nil
}
