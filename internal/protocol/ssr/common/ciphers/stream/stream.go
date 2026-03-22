package stream

import "crypto/cipher"

type IStreamCipher interface {
	KeyLen() int
	IVLen() int
	NewStream(key []byte, iv []byte, decryptOrEncrypt int) (cipher.Stream, error)
}

var streamCiphers = make(map[string]IStreamCipher)

func registerStreamCiphers(method string, c IStreamCipher) {
	streamCiphers[method] = c
}

func GetStreamCipher(method string) IStreamCipher {
	return streamCiphers[method]
}
