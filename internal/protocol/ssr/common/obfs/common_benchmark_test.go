package obfs

import "testing"

func BenchmarkConbineToBytesTLSLike(b *testing.B) {
	ver := []byte{0x03, 0x03}
	head := []byte{0x16, 0x03, 0x01}
	payload := make([]byte, 512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = conbineToBytes(head, ver, uint16(len(payload)), payload)
	}
}

func BenchmarkConbineToBytesMixed(b *testing.B) {
	rnd := make([]byte, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = conbineToBytes(byte(0x17), []byte{0x03, 0x03}, uint16(128), rnd, "tail")
	}
}
