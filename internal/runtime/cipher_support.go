package runtime

import "strings"

var cipherAlias = map[string]string{
	"aes-128-cfb":             "aes-128-cfb",
	"aes_128_cfb":             "aes-128-cfb",
	"aes-192-cfb":             "aes-192-cfb",
	"aes_192_cfb":             "aes-192-cfb",
	"aes-256-cfb":             "aes-256-cfb",
	"aes_256_cfb":             "aes-256-cfb",
	"aes-128-gcm":             "aes-128-gcm",
	"aes_128_gcm":             "aes-128-gcm",
	"aes-192-gcm":             "aes-192-gcm",
	"aes_192_gcm":             "aes-192-gcm",
	"aes-256-gcm":             "aes-256-gcm",
	"aes_256_gcm":             "aes-256-gcm",
	"chacha20":                "chacha20",
	"chacha20-ietf":           "chacha20-ietf",
	"chacha20_ietf":           "chacha20-ietf",
	"chacha20-ietf-poly1305":  "chacha20-ietf-poly1305",
	"chacha20_ietf_poly1305":  "chacha20-ietf-poly1305",
	"chacha20-poly1305":       "chacha20-ietf-poly1305",
	"chacha20_poly1305":       "chacha20-ietf-poly1305",
	"xchacha20-ietf-poly1305": "chacha20-ietf-poly1305",
	"xchacha20_ietf_poly1305": "chacha20-ietf-poly1305",
}

func normalizeCipherName(method string) string {
	key := strings.ToLower(strings.TrimSpace(method))
	if key == "" {
		return "aes-256-gcm"
	}
	if v, ok := cipherAlias[key]; ok {
		return v
	}
	return key
}

func isCipherSupported(method string) bool {
	if strings.TrimSpace(method) == "" {
		return true
	}
	_, ok := cipherAlias[normalizeCipherName(method)]
	return ok
}
