package obfs

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/cache"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/log"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/arrayx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/randomx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/stringx"
	"github.com/pkg/errors"
)

var (
	DefaultVersion     = []byte{0x03, 0x03}
	DefaultOverhead    = 5
	DefaultMaxTimeDiff = 60 * 60 * 24
)

func init() {
	registerMethod("tls1.2_ticket_auth", NewObfsTLS)
}

type AuthData struct {
	ServerInfo
	ClientData *cache.Cache
	ClientID   []byte
	StartTime  int
	TicketBuf  map[string][]byte
}

func NewObfsAuthData() *AuthData {
	return &AuthData{
		ServerInfo: NewServerInfo(),
		TicketBuf:  make(map[string][]byte),
		ClientID:   randomx.RandomBytes(32),
		ClientData: cache.New(60 * 5 * time.Second),
	}
}

type TLS struct {
	Plain
	*AuthData
	HandshakeStatus int
	SendBuffer      []byte
	RecvBuffer      []byte
	ClientID        []byte
	MaxTimeDiff     int
	TLSVersion      []byte
	Overhead        int
}

func NewObfsTLS(method string) (Plain, error) {
	newPlain, err := NewPlain(method)
	if err != nil {
		return nil, err
	}
	return &TLS{
		Plain:           newPlain,
		HandshakeStatus: 0,
		MaxTimeDiff:     DefaultMaxTimeDiff,
		TLSVersion:      DefaultVersion,
		Overhead:        DefaultOverhead,
		AuthData:        NewObfsAuthData(),
	}, nil
}

func (otls *TLS) GetOverhead(bool) int {
	return otls.Overhead
}

func (otls *TLS) GetServerInfo() ServerInfo {
	return otls.Plain.GetServerInfo()
}

func (otls *TLS) SetServerInfo(s ServerInfo) {
	otls.Plain.SetServerInfo(s)
}

func (otls *TLS) ClientPreEncrypt(buf []byte) ([]byte, error) {
	return otls.Plain.ClientPreEncrypt(buf)
}

func (otls *TLS) ClientEncode(buf []byte) ([]byte, error) {
	if otls.HandshakeStatus == -1 {
		return buf, nil
	}

	if (otls.HandshakeStatus & 8) == 8 {
		return otls.encodeAppDataRecords(buf), nil
	}
	if len(buf) > 0 {
		otls.SendBuffer = conbineToBytes(otls.SendBuffer, byte(0x17), otls.TLSVersion, uint16(len(buf)), buf)
	}
	if otls.HandshakeStatus == 0 {
		otls.HandshakeStatus = 1
		data := new(bytes.Buffer)
		ext := new(bytes.Buffer)
		binary.Write(data, binary.BigEndian, otls.TLSVersion)
		binary.Write(data, binary.BigEndian, otls.packAuthData(otls.AuthData.ClientID))
		binary.Write(data, binary.BigEndian, byte(0x20))
		binary.Write(data, binary.BigEndian, otls.AuthData.ClientID)
		binary.Write(data, binary.BigEndian, []byte{0x0, 0x1c, 0xc0, 0x2b, 0xc0, 0x2f, 0xcc, 0xa9, 0xcc, 0xa8, 0xcc, 0x14, 0xcc, 0x13, 0xc0, 0xa,
			0xc0, 0x14, 0xc0, 0x9, 0xc0, 0x13, 0x0, 0x9c, 0x0, 0x35, 0x0, 0x2f, 0x0, 0xa})
		binary.Write(data, binary.BigEndian, []byte{0x0, 0x01})

		binary.Write(ext, binary.BigEndian, []byte{0xff, 0x1, 0x0, 0x1, 0x0})

		var host string
		if otls.GetServerInfo().GetObfsParam() != "" {
			host = otls.GetServerInfo().GetObfsParam()
		} else {
			host = otls.GetServerInfo().GetHost()
		}

		if host != "" && stringx.IsDigit(string(host[len(host)-1])) {
			host = ""
		}

		hosts := strings.Split(host, ",")
		host = randomx.RandomStringsChoice(hosts)

		binary.Write(ext, binary.BigEndian, otls.sni(host))
		binary.Write(ext, binary.BigEndian, []byte{0x00, 0x17, 0x00, 0x00})

		if otls.AuthData.TicketBuf[host] == nil {
			otls.AuthData.TicketBuf[host] = randomx.RandomBytes((int(randomx.Uint16())%17 + 8) * 16)
		}
		binary.Write(ext, binary.BigEndian, conbineToBytes(
			[]byte{0x00, 0x23},
			len(otls.AuthData.TicketBuf[host]),
			otls.AuthData.TicketBuf[host]))

		binary.Write(ext, binary.BigEndian, MustHexDecode("000d001600140601060305010503040104030301030302010203"))
		binary.Write(ext, binary.BigEndian, MustHexDecode("000500050100000000"))
		binary.Write(ext, binary.BigEndian, MustHexDecode("00120000"))
		binary.Write(ext, binary.BigEndian, MustHexDecode("75500000"))
		binary.Write(ext, binary.BigEndian, MustHexDecode("000b00020100"))
		binary.Write(ext, binary.BigEndian, MustHexDecode("000a0006000400170018"))

		binary.Write(data, binary.BigEndian, conbineToBytes(
			uint16(ext.Len()),
			ext.Bytes()))

		result := conbineToBytes([]byte{0x01, 0x00}, uint16(data.Len()), data.Bytes())
		result = conbineToBytes([]byte{0x16, 0x03, 0x01}, uint16(len(result)), result)
		return result, nil
	} else if otls.HandshakeStatus == 1 && len(buf) == 0 {
		data := conbineToBytes(byte(0x14), otls.TLSVersion, []byte{0x00, 0x01, 0x01})
		data = conbineToBytes(data, byte(0x16), otls.TLSVersion, []byte{0x00, 0x20}, randomx.RandomBytes(22))
		data = conbineToBytes(data, hmacsha1(conbineToBytes(otls.GetServerInfo().GetKey(), otls.AuthData.ClientID), data)[:10])
		ret := conbineToBytes(data, otls.SendBuffer)
		otls.SendBuffer = []byte{}
		otls.HandshakeStatus = 8
		return ret, nil
	}

	return []byte{}, nil
}

func (otls *TLS) ClientDecode(buf []byte) ([]byte, bool, error) {
	if otls.HandshakeStatus == -1 {
		return buf, false, nil
	}
	if otls.HandshakeStatus == 8 {
		data, err := otls.decodeAppDataRecords(buf, false)
		if err != nil {
			log.Errorw("server decode appdata error", "data", hex.EncodeToString(otls.RecvBuffer))
			return nil, false, errors.New("server_decode appdata error")
		}
		return data, false, nil
	}

	if len(buf) < 11+32+1+32 {
		return nil, false, errors.New("client_decode data error")
	}

	verify := buf[11:33]
	if !bytes.Equal(hmacsha1(conbineToBytes(otls.GetServerInfo().GetKey(), otls.AuthData.ClientID), verify)[:10], buf[33:43]) {
		return nil, false, errors.New("client_decode data error")
	}
	if !bytes.Equal(hmacsha1(conbineToBytes(otls.GetServerInfo().GetKey(), otls.AuthData.ClientID), buf[:len(buf)-10])[:10], buf[len(buf)-10:]) {
		return nil, false, errors.New("client_decode data error")
	}
	return []byte{}, true, nil
}

func (otls *TLS) ServerEncode(buf []byte) ([]byte, error) {
	if otls.HandshakeStatus == -1 {
		return buf, nil
	}
	if (otls.HandshakeStatus & 8) == 8 {
		return otls.encodeAppDataRecords(buf), nil
	}

	otls.HandshakeStatus |= 8
	data := conbineToBytes(otls.TLSVersion, otls.packAuthData(otls.ClientID), byte(0x20), otls.ClientID, MustHexDecode("c02f000005ff01000100"))
	data = conbineToBytes([]byte{0x20, 0x00}, uint16(len(data)), data) // service hello
	data = conbineToBytes(byte(0x16), otls.TLSVersion, uint16(len(data)), data)
	if int(randomx.Float64Range(0, 8)) < 1 {
		ticket := randomx.RandomBytes(int((randomx.Uint16()%164)*2) + 64)
		ticket = conbineToBytes(uint16(len(ticket)+4), []byte{0x04, 0x00}, len(ticket), ticket)
		data = conbineToBytes(data, byte(0x16), otls.TLSVersion, ticket) // New session ticket
	}
	data = conbineToBytes(data, byte(0x14), otls.TLSVersion, []byte{0x00, 0x01, 0x01}) // ChangeCipherSpec
	finishLen := randomx.RandomIntChoice([]int{32, 40})
	data = conbineToBytes(data, byte(0x16), otls.TLSVersion, uint16(finishLen), randomx.RandomBytes(finishLen-10))
	data = conbineToBytes(data, hmacsha1(conbineToBytes(otls.GetServerInfo().GetKey(), otls.ClientID), data)[:10])
	if len(buf) != 0 {
		tmp, err := otls.ServerEncode(buf)
		if err != nil {
			return nil, err
		}
		data = conbineToBytes(data, tmp)
	}
	return data, nil
}

func (otls *TLS) ServerDecode(buf []byte) ([]byte, bool, bool, error) {
	if otls.HandshakeStatus == -1 {
		return buf, true, false, nil
	}
	if (otls.HandshakeStatus & 4) == 4 {
		data, err := otls.decodeAppDataRecords(buf, true)
		if err != nil {
			log.Errorw("server decode appdata error", "data", hex.EncodeToString(otls.RecvBuffer))
			return nil, false, false, errors.New("server_decode appdata error")
		}
		return data, true, false, nil
	}

	if (otls.HandshakeStatus & 1) == 1 {
		otls.RecvBuffer = conbineToBytes(otls.RecvBuffer, buf)
		buf = otls.RecvBuffer
		verify := buf
		if len(buf) < 11 {
			return nil, false, false, errors.New("server_decode data error")
		}

		if !matchBegin(buf, conbineToBytes(byte(0x14), otls.TLSVersion, []byte{0x00, 0x01, 0x01})) {
			return nil, false, false, errors.New("server_decode data error")
		}
		buf = buf[6:]
		if !matchBegin(buf, conbineToBytes(byte(0x16), otls.TLSVersion, byte(0x00))) {
			return nil, false, false, errors.New("server_decode data error")
		}

		verifyLen := binary.BigEndian.Uint16(buf[3:5]) + 1 // 11 - 10
		if len(verify) < int(verifyLen)+10 {
			return []byte{}, false, false, nil
		}
		if !bytes.Equal(hmacsha1(conbineToBytes(otls.GetServerInfo().GetKey(), otls.ClientID), verify[:verifyLen])[:10], verify[verifyLen:verifyLen+10]) {
			return nil, false, false, errors.New("server_decode data error")
		}
		otls.RecvBuffer = verify[verifyLen+10:]
		otls.HandshakeStatus |= 4
		return otls.ServerDecode([]byte{})
	}
	otls.RecvBuffer = conbineToBytes(otls.RecvBuffer, buf)
	buf = otls.RecvBuffer
	originBuf := buf
	if len(buf) < 3 {
		return []byte{}, false, false, nil
	}
	if !matchBegin(buf, []byte{0x16, 0x03, 0x01}) {
		return otls.DecodeErrorReturn(originBuf)
	}
	buf = buf[3:]
	headerLen := binary.BigEndian.Uint16(buf[:2])
	if headerLen > uint16(len(buf))-2 {
		return []byte{}, false, false, nil
	}

	otls.RecvBuffer = otls.RecvBuffer[headerLen+5:]
	otls.HandshakeStatus = 1
	buf = buf[2 : headerLen+2]
	if !matchBegin(buf, []byte{0x01, 0x00}) {
		log.Info("tls_auth not client hello message")
		return otls.DecodeErrorReturn(originBuf)
	}
	buf = buf[2:]
	if binary.BigEndian.Uint16(buf) != uint16(len(buf))-2 {
		log.Info("tls_auth wrong message size")
		return otls.DecodeErrorReturn(originBuf)
	}
	buf = buf[2:]
	if !matchBegin(buf, otls.TLSVersion) {
		log.Info("tls_auth wrong tls version")
		return otls.DecodeErrorReturn(originBuf)
	}
	buf = buf[2:]
	verifyId := buf[:32]
	buf = buf[32:]
	sessionLen := int8(buf[0])
	if sessionLen < 32 {
		log.Info("tls_auth wrong sessionid_len")
		return otls.DecodeErrorReturn(originBuf)
	}
	sessionId := buf[1 : sessionLen+1]
	otls.ClientID = sessionId
	sha1 := hmacsha1(conbineToBytes(otls.GetServerInfo().GetKey(), sessionId), verifyId[:22])[:10]
	utcTime := int(binary.BigEndian.Uint32(verifyId[:4]))
	timeDif := int(time.Now().Unix()) - utcTime

	if otls.GetServerInfo().GetObfsParam() != "" {
		dif, err := strconv.Atoi(otls.GetServerInfo().GetObfsParam())
		if err == nil {
			otls.MaxTimeDiff = dif
		}
	}

	if otls.MaxTimeDiff > 0 &&
		(timeDif < -otls.MaxTimeDiff ||
			timeDif > otls.MaxTimeDiff || int32(utcTime-otls.AuthData.StartTime) < int32(otls.MaxTimeDiff/2)) {
		log.Errorw("tls_auth wrong time",
			"receive_utc_time", uint32(utcTime),
			"now_unix", time.Now().Unix(),
			"time_diff", timeDif,
		)
		return otls.DecodeErrorReturn(originBuf)
	}

	if !bytes.Equal(sha1, verifyId[22:]) {
		log.Info("tls_auth wrong sha1")
		return otls.DecodeErrorReturn(originBuf)
	}

	if otls.ClientData.Get(string(verifyId[:22])) != nil {
		log.Infow("replay attack detect", "id", hex.EncodeToString(verifyId))
		return otls.DecodeErrorReturn(originBuf)
	}

	otls.ClientData.Put(string(verifyId[:22]), sessionId, time.Duration(DefaultMaxTimeDiff)*time.Second)
	if len(otls.RecvBuffer) >= 11 {
		ret, _, _, _ := otls.ServerDecode([]byte{})
		return ret, true, true, nil
	}
	return []byte{}, false, true, nil
}

func (otls *TLS) packAuthData(clientId []byte) []byte {
	dataBuf := new(bytes.Buffer)
	binary.Write(dataBuf, binary.BigEndian, uint32(time.Now().Unix()&0xFFFFFFFF))
	binary.Write(dataBuf, binary.BigEndian, randomx.RandomBytes(18))
	binary.Write(dataBuf, binary.BigEndian, hmacsha1(conbineToBytes(otls.Plain.GetServerInfo().GetKey(), clientId), dataBuf.Bytes())[:10])
	return dataBuf.Bytes()
}

func (otls *TLS) sni(host string) []byte {
	url := []byte(host)
	data := conbineToBytes([]byte{0x00}, uint16(len(url)), url)
	data = conbineToBytes([]byte{0x00, 0x00}, uint16(len(data)+2), uint16(len(data)), data)
	return data
}

func (otls *TLS) DecodeErrorReturn(buf []byte) ([]byte, bool, bool, error) {
	otls.HandshakeStatus = -1
	if otls.Overhead > 0 {
		otls.GetServerInfo().SetOverhead(otls.GetServerInfo().GetOverhead() - otls.Overhead)
	}
	otls.Overhead = 0
	if arrayx.FindStringInArray(otls.Plain.GetMethod(), []string{"tls1.2_ticket_auth", "tls1.2_ticket_fastauth"}) {
		return errorReply2048(), false, false, nil
	}

	return buf, true, false, nil
}

func (otls *TLS) encodeAppDataRecords(buf []byte) []byte {
	ret := make([]byte, 0, len(buf)+32)
	for len(buf) > 2048 {
		size := uint16(math.Min(float64(randomx.Uint16()%4096+100), float64(len(buf))))
		ret = conbineToBytes(ret, byte(0x17), otls.TLSVersion, size, buf[:size])
		buf = buf[size:]
	}
	if len(buf) > 0 {
		ret = conbineToBytes(ret, byte(0x17), otls.TLSVersion, uint16(len(buf)), buf)
	}
	return ret
}

func (otls *TLS) decodeAppDataRecords(incoming []byte, strictTLS12 bool) ([]byte, error) {
	ret := new(bytes.Buffer)
	otls.RecvBuffer = conbineToBytes(otls.RecvBuffer, incoming)
	for len(otls.RecvBuffer) > 5 {
		if int(otls.RecvBuffer[0]) != 0x17 {
			return nil, errors.New("server_decode appdata error")
		}
		if strictTLS12 && (int(otls.RecvBuffer[1]) != 0x03 || int(otls.RecvBuffer[2]) != 0x03) {
			return nil, errors.New("server_decode appdata error")
		}
		size := binary.BigEndian.Uint16(otls.RecvBuffer[3:5])
		if len(otls.RecvBuffer) < int(size)+5 {
			break
		}
		binary.Write(ret, binary.BigEndian, otls.RecvBuffer[5:size+5])
		otls.RecvBuffer = otls.RecvBuffer[size+5:]
	}
	return ret.Bytes(), nil
}
