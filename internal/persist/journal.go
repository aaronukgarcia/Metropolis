package persist

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Journal records are stored on disk as a sequence of self-describing,
// self-checking frames:
//
//	[4 bytes big-endian length L] [L bytes payload] [4 bytes IEEE CRC32 of (length header || payload)]
//
// The CRC covers the length header AND the payload together (not the
// payload alone). Folding the length in closes the zero-length torn-tail
// ambiguity an attacker found: an empty record encodes to 8 zero bytes,
// but crc32 of the four zero length-bytes is NOT zero, so a crash that
// zero-extends the file past synced data (len=0, crc=0) now fails the
// CRC check and is rejected as a torn tail rather than being silently
// promoted to a committed empty record. A genuinely-committed empty
// record still round-trips because encode and decode compute the CRC
// over the same length||payload span.
//
// This framing is what makes AC-5 (crash/torn-write safety) provable:
// a crash mid-append can only ever leave an INCOMPLETE frame at the
// tail of the file (fewer than 4+L+4 bytes present, or a length header
// whose declared payload runs past EOF, or a CRC mismatch). Every one
// of those conditions is detected by decodeFrames below and the
// incomplete/corrupt tail is silently excluded — never surfaced as a
// present-but-corrupt record, and never causing an earlier, complete
// record to be lost.
//
// A single call to (*os.File).Write with the fully-assembled frame
// buffer (see encodeFrame) is the unit of "one complete record" this
// package relies on being written as a unit before any crash-injection
// test manually truncates it.

const (
	frameLenSize = 4
	frameCRCSize = 4
)

// encodeFrame assembles one journal record into its on-disk frame.
func encodeFrame(payload []byte) []byte {
	buf := make([]byte, frameLenSize+len(payload)+frameCRCSize)
	binary.BigEndian.PutUint32(buf[0:frameLenSize], uint32(len(payload)))
	copy(buf[frameLenSize:frameLenSize+len(payload)], payload)
	// CRC covers the length header AND the payload together, so a
	// zero-filled torn tail (len=0, crc=0) is not mistaken for a
	// committed empty record.
	sum := crc32.ChecksumIEEE(buf[0 : frameLenSize+len(payload)])
	binary.BigEndian.PutUint32(buf[frameLenSize+len(payload):], sum)
	return buf
}

// decodeFrames parses as many complete, checksum-valid frames as
// possible from data, in order, and returns their payloads. Any
// trailing bytes that do not form a complete valid frame are silently
// ignored (the torn-write recovery behaviour AC-5 requires) — this
// function never returns an error for a torn tail, only for genuinely
// unrecoverable states, which in practice never occur here since every
// failure mode simply means "no more complete frames".
func decodeFrames(data []byte) [][]byte {
	var records [][]byte
	off := 0
	for {
		remaining := len(data) - off
		if remaining < frameLenSize {
			break // torn tail: not even a full length header present
		}
		l := binary.BigEndian.Uint32(data[off : off+frameLenSize])
		need := frameLenSize + int(l) + frameCRCSize
		if need < 0 || remaining < need {
			break // torn tail: declared payload/crc runs past EOF
		}
		payload := data[off+frameLenSize : off+frameLenSize+int(l)]
		wantCRC := binary.BigEndian.Uint32(data[off+frameLenSize+int(l) : off+need])
		// Verify the CRC over the length header || payload span — the
		// same bytes encodeFrame checksummed.
		if crc32.ChecksumIEEE(data[off:off+frameLenSize+int(l)]) != wantCRC {
			break // corrupt frame: stop here, do not skip past it
		}
		// Copy out — data may be a mmap'd or reused buffer in future
		// callers; never alias it into the returned slice.
		rec := make([]byte, len(payload))
		copy(rec, payload)
		records = append(records, rec)
		off += need
	}
	return records
}

// readAllFrames reads r fully and decodes every complete frame in it.
func readAllFrames(r io.Reader) ([][]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return decodeFrames(data), nil
}
