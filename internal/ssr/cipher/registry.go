package cipher

import "strings"

var supportedMethods = map[string]struct{}{
	"aes-128-cfb":            {},
	"aes-192-cfb":            {},
	"aes-256-cfb":            {},
	"aes-128-gcm":            {},
	"aes-192-gcm":            {},
	"aes-256-gcm":            {},
	"chacha20":               {},
	"chacha20-ietf":          {},
	"chacha20-ietf-poly1305": {},
	"none":                   {},
}

func NormalizeMethod(method string) string {
	m := strings.ToLower(strings.TrimSpace(method))
	m = strings.ReplaceAll(m, "_", "-")
	return m
}

func Supported(method string) bool {
	_, ok := supportedMethods[NormalizeMethod(method)]
	return ok
}
