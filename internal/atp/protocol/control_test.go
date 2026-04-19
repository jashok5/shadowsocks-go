package protocol

import "testing"

func TestOpenStreamPayloadRoundTrip(t *testing.T) {
	origin := OpenStreamRequest{Network: NetworkUDP, Host: "1.1.1.1", Port: 53}
	b, err := EncodeOpenStreamPayload(origin)
	if err != nil {
		t.Fatalf("encode open payload: %v", err)
	}
	decoded, err := DecodeOpenStreamPayload(b)
	if err != nil {
		t.Fatalf("decode open payload: %v", err)
	}
	if decoded.Network != origin.Network || decoded.Host != origin.Host || decoded.Port != origin.Port {
		t.Fatalf("decoded mismatch: got=%+v want=%+v", decoded, origin)
	}
}

func TestOpenStreamPayloadRejectsInvalid(t *testing.T) {
	if _, err := EncodeOpenStreamPayload(OpenStreamRequest{Network: 9, Host: "x", Port: 1}); err == nil {
		t.Fatalf("expected invalid network error")
	}
	if _, err := EncodeOpenStreamPayload(OpenStreamRequest{Network: NetworkTCP, Host: "", Port: 1}); err == nil {
		t.Fatalf("expected missing host error")
	}
	if _, err := DecodeOpenStreamPayload(nil); err == nil {
		t.Fatalf("expected decode error")
	}
}
