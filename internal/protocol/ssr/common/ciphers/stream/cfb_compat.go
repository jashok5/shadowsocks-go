package stream

import "crypto/cipher"

type cfbCompat struct {
	b       cipher.Block
	next    []byte
	out     []byte
	decrypt bool
}

func newCFBCompat(block cipher.Block, iv []byte, decrypt bool) cipher.Stream {
	bs := block.BlockSize()
	next := make([]byte, bs)
	copy(next, iv[:bs])
	return &cfbCompat{
		b:       block,
		next:    next,
		out:     make([]byte, bs),
		decrypt: decrypt,
	}
}

func (c *cfbCompat) XORKeyStream(dst, src []byte) {
	bs := len(c.out)
	for len(src) > 0 {
		c.b.Encrypt(c.out, c.next)
		n := bs
		if n > len(src) {
			n = len(src)
		}
		if c.decrypt {
			cipherText := append([]byte(nil), src[:n]...)
			for i := 0; i < n; i++ {
				dst[i] = src[i] ^ c.out[i]
			}
			copy(c.next, c.next[n:])
			copy(c.next[bs-n:], cipherText)
		} else {
			for i := 0; i < n; i++ {
				dst[i] = src[i] ^ c.out[i]
			}
			copy(c.next, c.next[n:])
			copy(c.next[bs-n:], dst[:n])
		}
		dst = dst[n:]
		src = src[n:]
	}
}
