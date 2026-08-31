package persist

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeFrameRoundTrip(t *testing.T) {
	payloads := [][]byte{
		[]byte(""),
		[]byte("a"),
		[]byte("hello, journal"),
		bytes.Repeat([]byte("z"), 4096),
	}
	var buf []byte
	for _, p := range payloads {
		buf = append(buf, encodeFrame(p)...)
	}
	got := decodeFrames(buf)
	if len(got) != len(payloads) {
		t.Fatalf("decoded %d frames, want %d", len(got), len(payloads))
	}
	for i, p := range payloads {
		if !bytes.Equal(got[i], p) {
			t.Fatalf("frame %d = %q, want %q", i, got[i], p)
		}
	}
}

func TestDecodeFramesStopsAtTornTail(t *testing.T) {
	good := append(encodeFrame([]byte("one")), encodeFrame([]byte("two"))...)
	full := append(good, encodeFrame([]byte("three-never-finishes"))...)

	for cut := len(good) + 1; cut < len(full); cut++ {
		got := decodeFrames(full[:cut])
		if len(got) != 2 {
			t.Fatalf("cut at %d: decoded %d frames, want exactly 2 (the two complete ones)", cut, len(got))
		}
		if string(got[0]) != "one" || string(got[1]) != "two" {
			t.Fatalf("cut at %d: complete frames corrupted: %v", cut, got)
		}
	}
}

func TestDecodeFramesDetectsCRCCorruption(t *testing.T) {
	frame := encodeFrame([]byte("payload"))
	// Flip a byte inside the payload region without touching the CRC —
	// this must be detected and the frame excluded, not returned as a
	// silently-wrong record.
	corrupt := make([]byte, len(frame))
	copy(corrupt, frame)
	corrupt[frameLenSize] ^= 0xFF

	got := decodeFrames(corrupt)
	if len(got) != 0 {
		t.Fatalf("corrupted frame decoded as %d records, want 0 (CRC mismatch must exclude it)", len(got))
	}
}

func TestDecodeFramesEmptyInput(t *testing.T) {
	got := decodeFrames(nil)
	if len(got) != 0 {
		t.Fatalf("decodeFrames(nil) = %v, want empty", got)
	}
}
