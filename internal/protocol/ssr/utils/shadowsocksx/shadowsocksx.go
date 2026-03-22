package shadowsocksx

import (
	"crypto/sha1"
	"io"

	"golang.org/x/crypto/hkdf"
)

func HkdfSha1(secret, salt, info, key []byte) error {
	_, err := io.ReadFull(hkdf.New(sha1.New, secret, salt, info), key)
	return err
}
