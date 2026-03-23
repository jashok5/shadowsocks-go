package stream

import (
	"crypto/aes"
	"crypto/cipher"
)

func init() {
	registerStreamCiphers("aes-128-cfb", &aesCfb{16, 16})
	registerStreamCiphers("aes-192-cfb", &aesCfb{24, 16})
	registerStreamCiphers("aes-256-cfb", &aesCfb{32, 16})
}

type aesCfb struct {
	keyLen int
	ivLen  int
}

func (a *aesCfb) KeyLen() int {
	return a.keyLen
}
func (a *aesCfb) IVLen() int {
	return a.ivLen
}
func (a *aesCfb) NewStream(key, iv []byte, decryptOrEncrypt int) (cipher.Stream, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if decryptOrEncrypt == 0 {
		return newCFBCompat(block, iv, false), nil
	}

	return newCFBCompat(block, iv, true), nil
}
