package server

import (
	"errors"
	"strconv"
	"strings"
)

// rangeStatus describes the outcome of parsing the HTTP Range request header.
type rangeStatus int

const (
	// rangeNone means no Range header was supplied; serve the whole payload.
	rangeNone rangeStatus = iota
	// rangeOK means a single satisfiable byte range was extracted.
	rangeOK
	// rangeUnsat means the header was syntactically valid but cannot be served
	// against an object of the given size (e.g. start beyond EOF).
	rangeUnsat
)

// errMultiRange is returned when the header requests more than one range; we
// deliberately do not implement multipart/byteranges responses.
var errMultiRange = errors.New("multi-range requests are not supported")

// parseRange parses a single-range RFC 7233 Range header against an object of
// totalSize bytes and returns the (offset, length) pair to forward to NeoFS.
//
// Supported forms:
//
//	bytes=a-b   -> [a, b]
//	bytes=a-    -> [a, totalSize-1]
//	bytes=-n    -> last n bytes
//
// On success returns rangeOK with the actual byte window, clamped to the
// object size. Empty header -> rangeNone. Out-of-range start -> rangeUnsat.
// Multi-range or malformed headers return an error.
func parseRange(header string, totalSize uint64) (offset, length uint64, status rangeStatus, err error) {
	if header == "" {
		return 0, 0, rangeNone, nil
	}

	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, rangeUnsat, errors.New("unsupported range unit")
	}
	spec := strings.TrimPrefix(header, prefix)
	if spec == "" {
		return 0, 0, rangeUnsat, errors.New("empty range")
	}
	if strings.Contains(spec, ",") {
		return 0, 0, rangeUnsat, errMultiRange
	}

	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, rangeUnsat, errors.New("missing '-' in range")
	}
	startStr := strings.TrimSpace(spec[:dash])
	endStr := strings.TrimSpace(spec[dash+1:])

	if totalSize == 0 {
		return 0, 0, rangeUnsat, nil
	}

	switch {
	case startStr == "" && endStr == "":
		return 0, 0, rangeUnsat, errors.New("empty range")

	case startStr == "":
		// bytes=-N: last N bytes.
		n, perr := strconv.ParseUint(endStr, 10, 64)
		if perr != nil || n == 0 {
			return 0, 0, rangeUnsat, errors.New("invalid suffix length")
		}
		if n > totalSize {
			n = totalSize
		}
		return totalSize - n, n, rangeOK, nil

	case endStr == "":
		// bytes=N-: from N to end.
		start, perr := strconv.ParseUint(startStr, 10, 64)
		if perr != nil {
			return 0, 0, rangeUnsat, errors.New("invalid start offset")
		}
		if start >= totalSize {
			return 0, 0, rangeUnsat, nil
		}
		return start, totalSize - start, rangeOK, nil

	default:
		// bytes=A-B inclusive.
		start, perr := strconv.ParseUint(startStr, 10, 64)
		if perr != nil {
			return 0, 0, rangeUnsat, errors.New("invalid start offset")
		}
		end, perr := strconv.ParseUint(endStr, 10, 64)
		if perr != nil {
			return 0, 0, rangeUnsat, errors.New("invalid end offset")
		}
		if end < start || start >= totalSize {
			return 0, 0, rangeUnsat, nil
		}
		if end >= totalSize {
			end = totalSize - 1
		}
		return start, end - start + 1, rangeOK, nil
	}
}
