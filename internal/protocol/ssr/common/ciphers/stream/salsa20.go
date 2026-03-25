package stream

import (
	"crypto/cipher"
	"encoding/binary"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/pool"
	xsalsa20 "golang.org/x/crypto/salsa20"
)

func init() {
	registerStreamCiphers("salsa20", &salsa20{32, 8})
}

type salsa20 struct {
	keyLen int
	ivLen  int
}

func (a *salsa20) KeyLen() int {
	return a.keyLen
}
func (a *salsa20) IVLen() int {
	return a.ivLen
}
func (a *salsa20) NewStream(key, iv []byte, _ int) (cipher.Stream, error) {
	var c salsaStreamCipher
	copy(c.nonce[:], iv[:8])
	copy(c.key[:], key[:32])
	return &c, nil
}

type salsaStreamCipher struct {
	nonce   [8]byte
	key     [32]byte
	counter int
}

func (c *salsaStreamCipher) XORKeyStream(dst, src []byte) {
	var buf []byte
	padLen := c.counter % 64
	dataSize := len(src) + padLen
	if cap(dst) >= dataSize {
		buf = dst[:dataSize]
	} else if pool.BufferSize >= dataSize {
		buf = pool.GetBuf()
		defer pool.PutBuf(buf)
		buf = buf[:dataSize]
	} else {
		buf = make([]byte, dataSize)
	}

	var subNonce [16]byte
	copy(subNonce[:], c.nonce[:])
	binary.LittleEndian.PutUint64(subNonce[len(c.nonce):], uint64(c.counter/64))

	copy(buf[padLen:], src[:])
	xsalsa20.XORKeyStream(buf, buf, subNonce[:], &c.key)
	copy(dst, buf[padLen:])

	c.counter += len(src)
}
