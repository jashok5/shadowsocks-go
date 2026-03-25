package obfs

import (
	"math"
	"strconv"
	"strings"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/binaryx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/bytesx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/randomx"
)

func parseProtocolParamUIDSecret(param string) (uid int, secret string, ok bool, err error) {
	items := strings.SplitN(param, ":", 2)
	if len(items) != 2 {
		return 0, "", false, nil
	}
	uid, err = strconv.Atoi(items[0])
	if err != nil {
		return 0, "", false, err
	}
	return uid, items[1], true, nil
}

func parseProtocolParamUIDSecretPack(param string) (uidPack []byte, secret string, ok bool, err error) {
	uid, secret, ok, err := parseProtocolParamUIDSecret(param)
	if err != nil || !ok {
		return nil, "", ok, err
	}
	return binaryx.LEUint32ToBytes(uint32(uid)), secret, true, nil
}

func resolveUserKey(users map[string]string, uid, key, recvIV []byte, convert func(string) []byte) []byte {
	if pass, ok := users[string(uid)]; ok {
		return convert(pass)
	}
	if len(users) == 0 {
		return key
	}
	return recvIV
}

func resolveUDPEncryptTarget(users map[string]string, uid, key, recvIV []byte, convert func(string) []byte) ([]byte, []byte) {
	if pass, ok := users[string(uid)]; ok {
		return uid, convert(pass)
	}
	if len(users) == 0 {
		return nil, key
	}
	return nil, recvIV
}

func findRequiredUserKey(users map[string]string, uidPack []byte, convert func(string) []byte) ([]byte, bool) {
	pass, ok := users[string(uidPack)]
	if !ok {
		return nil, false
	}
	return convert(pass), true
}

func buildClientPreEncrypt(buf []byte, hasSentHeader *bool, getHeadSize func([]byte, int) int, authData []byte, packAuthData func([]byte, []byte) ([]byte, error), unitLen int, packChunk func([]byte) ([]byte, error)) ([]byte, error) {
	result := []byte{}
	if !*hasSentHeader {
		headSize := getHeadSize(buf, 30)
		dataLen := int(math.Min(float64(len(buf)), float64(randomx.RandIntRange(0, 31)+headSize)))
		header, err := packAuthData(authData, buf[:dataLen])
		if err != nil {
			return nil, err
		}
		result = bytesx.ContactSlice(result, header)
		buf = buf[dataLen:]
		*hasSentHeader = true
	}
	for len(buf) > unitLen {
		chunk, err := packChunk(buf[:unitLen])
		if err != nil {
			return nil, err
		}
		result = bytesx.ContactSlice(result, chunk)
		buf = buf[unitLen:]
	}
	chunk, err := packChunk(buf)
	if err != nil {
		return nil, err
	}
	result = bytesx.ContactSlice(result, chunk)
	return result, nil
}
