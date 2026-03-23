package stream

import (
	"crypto/cipher"

	"golang.org/x/crypto/cast5"
)

func init() {
	registerStreamCiphers("cast5-cfb", &cast5Cfb{16, 8})
}

type cast5Cfb struct {
	keyLen int
	ivLen  int
}

func (a *cast5Cfb) KeyLen() int {
	return a.keyLen
}
func (a *cast5Cfb) IVLen() int {
	return a.ivLen
}
func (a *cast5Cfb) NewStream(key, iv []byte, decryptOrEncrypt int) (cipher.Stream, error) {
	block, err := cast5.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if decryptOrEncrypt == 0 {
		return newCFBCompat(block, iv, false), nil
	}

	return newCFBCompat(block, iv, true), nil
}
