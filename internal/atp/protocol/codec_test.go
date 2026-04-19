package protocol

import (
	"bytes"
	"testing"
)

func TestWriteReadFrameRoundTrip(t *testing.T) {
	origin := &Frame{
		Header: Header{
			Magic:     Magic,
			Version:   VersionV1,
			Type:      TypeData,
			Flags:     1,
			SessionID: 42,
			StreamID:  7,
			Seq:       100,
		},
		Payload: []byte("hello-atp"),
	}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, origin); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	decoded, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	if decoded.Header.Magic != Magic || decoded.Header.Version != VersionV1 || decoded.Header.Type != TypeData {
		t.Fatalf("unexpected header: %+v", decoded.Header)
	}
	if decoded.Header.SessionID != 42 || decoded.Header.StreamID != 7 || decoded.Header.Seq != 100 {
		t.Fatalf("unexpected ids: %+v", decoded.Header)
	}
	if string(decoded.Payload) != "hello-atp" {
		t.Fatalf("unexpected payload: %q", string(decoded.Payload))
	}
}

func TestBadHeaderCRCFails(t *testing.T) {
	h := Header{Magic: Magic, Version: VersionV1, Type: TypePing}
	hb, err := h.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	hb[24] ^= 0xFF

	var out Header
	err = out.UnmarshalBinary(hb)
	if err == nil {
		t.Fatalf("expected crc error")
	}
}
