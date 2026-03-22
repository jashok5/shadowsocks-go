package block

import (
	"crypto/aes"
	"crypto/cipher"
)

func init() {
	registerBlockCiphers("aes-128-cbc", &aesCbc{16, 16})
}

type aesCbc struct {
	keyLen int
	ivLen  int
}

func (a *aesCbc) KeyLen() int {
	return a.keyLen
}
func (a *aesCbc) IVLen() int {
	return a.ivLen
}
func (a *aesCbc) NewBlock(key, iv []byte, decryptOrEncrypt int) (cipher.BlockMode, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if decryptOrEncrypt == 0 {
		return cipher.NewCBCEncrypter(block, iv), nil
	}
	return cipher.NewCBCDecrypter(block, iv), nil
}
