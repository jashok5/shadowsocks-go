package stream

import (
	"crypto/cipher"
	"crypto/des"
)

func init() {
	registerStreamCiphers("des-cfb", &desCfb{8, 8})
}

type desCfb struct {
	keyLen int
	ivLen  int
}

func (a *desCfb) KeyLen() int {
	return a.keyLen
}
func (a *desCfb) IVLen() int {
	return a.ivLen
}
func (a *desCfb) NewStream(key, iv []byte, decryptOrEncrypt int) (cipher.Stream, error) {
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if decryptOrEncrypt == 0 {
		return newCFBCompat(block, iv, false), nil
	}

	return newCFBCompat(block, iv, true), nil
}
