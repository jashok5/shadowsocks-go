package runtime

import "testing"

func TestNormalizeCipherName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "AES_256_GCM", want: "aes-256-gcm"},
		{in: "chacha20_poly1305", want: "chacha20-ietf-poly1305"},
		{in: "xchacha20-ietf-poly1305", want: "chacha20-ietf-poly1305"},
		{in: "aes-128-gcm", want: "aes-128-gcm"},
	}
	for _, tt := range tests {
		if got := normalizeCipherName(tt.in); got != tt.want {
			t.Fatalf("normalizeCipherName(%q)=%q want=%q", tt.in, got, tt.want)
		}
	}
}

func TestCipherSupport(t *testing.T) {
	if !isCipherSupported("AES_256_GCM") {
		t.Fatalf("expected AES_256_GCM supported")
	}
	if isCipherSupported("rc4-md5") {
		t.Fatalf("expected rc4-md5 unsupported")
	}
}
