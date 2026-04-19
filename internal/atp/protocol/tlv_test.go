package protocol

import "testing"

func TestTLVRoundTrip(t *testing.T) {
	in := []TLV{
		{Type: 1, Value: []byte("client")},
		{Type: 2, Value: []byte{1, 2, 3, 4}},
	}
	b, err := EncodeTLVs(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeTLVs(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len mismatch: got=%d want=%d", len(out), len(in))
	}
	if out[0].Type != 1 || string(out[0].Value) != "client" {
		t.Fatalf("unexpected first tlv")
	}
	if out[1].Type != 2 || len(out[1].Value) != 4 {
		t.Fatalf("unexpected second tlv")
	}
}
