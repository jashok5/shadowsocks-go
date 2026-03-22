package stream

import (
	"crypto/aes"
	"crypto/cipher"
)

func init() {
	registerStreamCiphers("aes-128-ctr", &aesCtr{16, 16})
	registerStreamCiphers("aes-192-ctr", &aesCtr{24, 16})
	registerStreamCiphers("aes-256-ctr", &aesCtr{32, 16})
}

type aesCtr struct {
	keyLen int
	ivLen  int
}

func (a *aesCtr) KeyLen() int {
	return a.keyLen
}
func (a *aesCtr) IVLen() int {
	return a.ivLen
}
func (a *aesCtr) NewStream(key, iv []byte, _ int) (cipher.Stream, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewCTR(block, iv), nil
}
