package stream

import (
	"crypto/cipher"
	"crypto/rc4"
)

func init() {
	registerStreamCiphers("rc4", &rc4Cryptor{16, 0})

}

type rc4Cryptor struct {
	keyLen int
	ivLen  int
}

func (a *rc4Cryptor) KeyLen() int {
	return a.keyLen
}
func (a *rc4Cryptor) IVLen() int {
	return a.ivLen
}

func (a *rc4Cryptor) NewStream(key, _ []byte, _ int) (cipher.Stream, error) {
	block, err := rc4.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return block, nil
}
