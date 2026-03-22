package stream

import (
	"crypto/cipher"

	"golang.org/x/crypto/blowfish"
)

func init() {
	registerStreamCiphers("bf-cfb", &bfCfb{16, 8})
}

type bfCfb struct {
	keyLen int
	ivLen  int
}

func (a *bfCfb) KeyLen() int {
	return a.keyLen
}
func (a *bfCfb) IVLen() int {
	return a.ivLen
}
func (a *bfCfb) NewStream(key, iv []byte, decryptOrEncrypt int) (cipher.Stream, error) {
	block, err := blowfish.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if decryptOrEncrypt == 0 {
		return cipher.NewCFBEncrypter(block, iv), nil
	}

	return cipher.NewCFBDecrypter(block, iv), nil
}
