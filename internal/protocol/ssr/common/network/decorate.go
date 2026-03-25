package network

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/ciphers"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/log"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/obfs"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/core"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/addrx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/binaryx"
	"github.com/pkg/errors"
)

type ILimiter interface {
	Wait(int, int) error
	DownLimit(int, int) error
	UpLimit(int, int) error
}

func NewShadowsocksRDecorate(request *Request, obfsMethod, cryptMethod, key, protocolMethod, obfsParam, protocolParam, host string, port int, isLocal bool, single int, users map[string]string, obfsService core.ObfsProtocolService) (ssrd *ShadowsocksRDecorate, err error) {
	if obfsService == nil {
		return nil, fmt.Errorf("obfs protocol service is nil")
	}
	ssrd = &ShadowsocksRDecorate{
		Request:       request,
		ObfsParam:     obfsParam,
		ProtocolParam: protocolParam,
		Host:          host,
		Port:          port,
		ISLocal:       isLocal,
		Users:         users,
		single:        single,
		recvBuf:       new(bytes.Buffer),
	}

	ssrd.obfs, err = obfs.GetObfs(obfsMethod)
	if err != nil {
		return nil, err
	}

	ssrd.protocol, err = obfs.GetObfs(protocolMethod)
	if err != nil {
		return nil, err
	}

	ssrd.encryptor, err = ciphers.NewEncryptor(cryptMethod, key)
	if err != nil {
		return nil, err
	}

	ssrd.Overhead = ssrd.obfs.GetOverhead(isLocal) + ssrd.protocol.GetOverhead(isLocal)

	ssrd.obfs.SetServerInfo(ssrd.getServerInfo(true, obfsService))
	ssrd.protocol.SetServerInfo(ssrd.getServerInfo(false, obfsService))

	if single != 1 {
		ssrd.UID = port
	}
	return ssrd, err
}

type ShadowsocksRDecorate struct {
	*Request
	UID           int
	obfs          obfs.Plain
	protocol      obfs.Plain
	encryptor     *ciphers.Encryptor
	Host          string
	Port          int
	ObfsParam     string
	ProtocolParam string
	Users         map[string]string
	Overhead      int
	ISLocal       bool
	recvBuf       *bytes.Buffer
	readBuf       []byte
	udpReadBuf    []byte
	upload        int64
	download      int64
	single        int
	common.TrafficReport
	ILimiter
	*sync.Mutex
}

const (
	defaultTCPReadBufSize = 4 * 1024
	defaultUDPReadBufSize = 2048
)

func (ssrd *ShadowsocksRDecorate) SetLimter(limiter ILimiter) {
	ssrd.ILimiter = limiter
}

func (ssrd *ShadowsocksRDecorate) Read(buf []byte) (n int, err error) {
	defer func() {
		if ssrd.ILimiter != nil {
			if err := ssrd.ILimiter.UpLimit(ssrd.UID, n); err != nil {
				log.Errorw("up limiter failed", log.FieldUID, ssrd.UID, log.FieldError, err)
			}
		}
	}()

	if ssrd.recvBuf.Len() > 0 {
		return ssrd.recvBuf.Read(buf)
	}

	if cap(ssrd.readBuf) < defaultTCPReadBufSize {
		ssrd.readBuf = make([]byte, defaultTCPReadBufSize)
	}
	bufTmp := ssrd.readBuf[:defaultTCPReadBufSize]
	n, err = ssrd.Conn.Read(bufTmp)
	if err != nil {
		return 0, err
	}
	atomic.AddInt64(&ssrd.upload, int64(n))

	data := bufTmp[:n]
	unobfsData, needDecrypt, needSendBack, err := ssrd.obfs.ServerDecode(data)
	if log.DebugEnabled() {
		log.Debugw("shadowsocksr obfs ServerDecode",
			log.FieldRequestID, ssrd.RequestID,
			log.FieldData, hex.EncodeToString(data),
			"unobfs_data", hex.EncodeToString(unobfsData),
			"need_decrypt", needDecrypt,
			"need_send_back", needSendBack,
		)
	}

	if err != nil {
		if log.DebugEnabled() {
			log.Debugw("ShadowsocksRDecorate obfs decrypt error", log.FieldError, err)
		}
		return 0, fmt.Errorf("[%s] shadowsocksr obfs decrypt error", ssrd.RequestID)
	}

	if needSendBack {
		result, err := ssrd.obfs.ServerEncode([]byte{})
		if err != nil {
			return 0, err
		}
		n, err = ssrd.Conn.Write(result)
		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate obfs sendback error.", ssrd.RequestID))
		}
		atomic.AddInt64(&ssrd.download, int64(n))
		return ssrd.Read(buf)
	}

	if needDecrypt {
		cleartext, err := ssrd.encryptor.Decrypt(unobfsData)
		if ssrd.protocol.GetServerInfo().GetRecvIv() == nil || len(ssrd.protocol.GetServerInfo().GetRecvIv()) == 0 {
			ssrd.protocol.GetServerInfo().SetRecvIv(ssrd.encryptor.IVIn)
		}
		if log.DebugEnabled() {
			log.Debugw("ShadowsocksRDecorate encryptor decrypt",
				log.FieldRequestID, ssrd.RequestID,
				"cleartext_hex", hex.EncodeToString(cleartext),
			)
		}

		if err != nil && strings.Contains(err.Error(), "buf is too short") {
			return ssrd.Read(buf)
		}

		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate obfs decrypt error.", ssrd.RequestID))
		}
		data = cleartext
	} else {
		data = unobfsData
	}

	data, sendback, err := ssrd.protocol.ServerPostDecrypt(data)
	if log.DebugEnabled() {
		log.Debugw("ShadowsocksRDecorate protocol server post decrypt",
			log.FieldRequestID, ssrd.RequestID,
			"server_post_decrypt_hex", hex.EncodeToString(data),
			"send_back", sendback,
		)
	}
	if err != nil {
		return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate protocol post decrypt error.", ssrd.RequestID))
	}
	if sendback {
		backdata, err := ssrd.protocol.ServerPreEncrypt([]byte{})
		if log.DebugEnabled() {
			log.Debugw("shadowoscksr read server pre encrypt",
				log.FieldRequestID, ssrd.RequestID,
				"back_data", hex.EncodeToString(backdata),
			)
		}
		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate protocol pre encode error.", ssrd.RequestID))
		}
		backdata, err = ssrd.encryptor.Encrypt(backdata)
		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate encrypter encrypt error.", ssrd.RequestID))
		}
		if log.DebugEnabled() {
			log.Debugw("shadowoscksr read encrypt",
				log.FieldRequestID, ssrd.RequestID,
				"read_encrypt_data", hex.EncodeToString(backdata),
			)
		}
		backdata, err = ssrd.obfs.ServerEncode(backdata)
		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate obfs service encode error.", ssrd.RequestID))
		}
		if log.DebugEnabled() {
			log.Debugw("shadowoscksr read server encode",
				log.FieldRequestID, ssrd.RequestID,
				"read_server_encode_data", hex.EncodeToString(backdata),
			)
		}
		n, err = ssrd.Conn.Write(backdata)
		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate obfs sendback error.", ssrd.RequestID))
		}
		atomic.AddInt64(&ssrd.download, int64(n))
	}
	if ssrd.TrafficReport != nil && ssrd.UID != 0 && ssrd.upload != 0 {
		upload := atomic.SwapInt64(&ssrd.upload, 0)
		if upload != 0 {
			ssrd.TrafficReport.Upload(ssrd.UID, upload)
		}
	}
	if ssrd.recvBuf.Len() == 0 && len(data) == 0 {
		return 0, nil
	}
	ssrd.recvBuf.Write(data)
	n, err = ssrd.recvBuf.Read(buf)

	return n, err
}

func (ssrd *ShadowsocksRDecorate) Write(buf []byte) (n int, err error) {
	defer func() {
		if ssrd.ILimiter != nil {
			if err := ssrd.ILimiter.DownLimit(ssrd.UID, n); err != nil {
				log.Errorw("down limiter failed", log.FieldUID, ssrd.UID, log.FieldError, err)
			}
		}
	}()

	data, err := ssrd.protocol.ServerPreEncrypt(buf)
	if err != nil {
		return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate protocol service encode error.", ssrd.RequestID))
	}

	data, err = ssrd.encryptor.Encrypt(data)
	if err != nil {
		return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate encryptor encrypt error.", ssrd.RequestID))
	}

	data, err = ssrd.obfs.ServerEncode(data)
	if err != nil {
		return 0, errors.Wrap(err, fmt.Sprintf("[%s] ShadowsocksRDecorate obfs service encode error.", ssrd.RequestID))
	}

	n, err = ssrd.Conn.Write(data)
	if err != nil {
		return 0, err
	}
	atomic.AddInt64(&ssrd.download, int64(n))
	if ssrd.TrafficReport != nil && ssrd.download != 0 && ssrd.UID != 0 {
		download := atomic.SwapInt64(&ssrd.download, 0)
		if download != 0 {
			ssrd.TrafficReport.Download(ssrd.UID, download)
		}
	}

	return len(buf), nil
}

func (ssrd *ShadowsocksRDecorate) ReadFrom() (data, uid []byte, addr net.Addr, err error) {
	if cap(ssrd.udpReadBuf) < defaultUDPReadBufSize {
		ssrd.udpReadBuf = make([]byte, defaultUDPReadBufSize)
	}
	p := ssrd.udpReadBuf[:defaultUDPReadBufSize]
	n, addr, err := ssrd.PacketConn.ReadFrom(p)
	if err != nil {
		return nil, nil, nil, err
	}
	data, iv, err := ssrd.encryptor.DecryptAll(p[:n])
	if err != nil {
		return nil, nil, nil, err
	}
	ssrd.protocol.GetServerInfo().SetIv(iv)
	result, uidPack, err := ssrd.protocol.ServerUDPPostDecrypt(data)
	if err != nil {
		return nil, nil, nil, err
	}

	if ssrd.single == 1 && ssrd.TrafficReport != nil {
		ssrd.TrafficReport.Upload(int(binaryx.LEBytesToUInt32([]byte(uidPack))), int64(n))
	}
	if ssrd.single != 1 && ssrd.TrafficReport != nil {
		ssrd.TrafficReport.Upload(ssrd.UID, int64(n))
		uidPack = string(binaryx.LEUint32ToBytes(uint32(ssrd.UID)))
	}
	return result, []byte(uidPack), addr, err

}

func (ssrd *ShadowsocksRDecorate) WriteTo(p, uid []byte, addr net.Addr) error {
	data, err := ssrd.protocol.ServerUDPPreEncrypt(p, uid)
	if err != nil {
		return err
	}
	data, err = ssrd.encryptor.EncryptAll(data, ssrd.encryptor.MustNewIV())
	if err != nil {
		return err
	}
	n, err := ssrd.Request.WriteTo(data, addr)
	if ssrd.TrafficReport != nil {
		ssrd.TrafficReport.Download(int(binaryx.LEBytesToUInt32(uid)), int64(n))
	}
	return err
}

func (ssrd *ShadowsocksRDecorate) getServerInfo(isObfs bool, obfsService core.ObfsProtocolService) obfs.ServerInfo {
	serverInfo := obfs.NewServerInfo()
	serverInfo.SetHost(ssrd.Host)
	serverInfo.SetPort(ssrd.Port)
	if ssrd.Conn != nil {
		serverInfo.SetClient(net.ParseIP(addrx.GetIPFromAddr(ssrd.Conn.RemoteAddr())))
		serverInfo.SetPort(addrx.GetPortFromAddr(ssrd.Conn.RemoteAddr()))
	}
	if isObfs {
		serverInfo.SetObfsParam(ssrd.ObfsParam)
		serverInfo.SetProtocolParam("")
	} else {
		serverInfo.SetObfsParam("")
		serverInfo.SetProtocolParam(ssrd.ProtocolParam)
	}
	serverInfo.SetIv(ssrd.encryptor.IVOut)
	serverInfo.SetRecvIv([]byte{})
	serverInfo.SetKeyStr(ssrd.encryptor.KeyStr)
	serverInfo.SetKey(ssrd.encryptor.Key)
	serverInfo.SetHeadLen(obfs.DefaultHeadLen)
	serverInfo.SetTCPMss(detectTCPMSS(ssrd.Conn))
	serverInfo.SetBufferSize(obfs.BufSize - ssrd.Overhead)
	serverInfo.SetOverhead(ssrd.Overhead)
	serverInfo.SetUpdateUserFunc(ssrd.UpdateUser)
	serverInfo.SetUsers(ssrd.Users)
	serverInfo.SetObfsProtocolService(obfsService)
	return serverInfo
}

func detectTCPMSS(conn net.Conn) int {
	if conn == nil {
		return obfs.TcpMss
	}

	tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok || tcpAddr == nil || tcpAddr.IP == nil {
		return obfs.TcpMss
	}

	mtu := detectInterfaceMTUByIP(tcpAddr.IP)
	if mtu <= 0 {
		return obfs.TcpMss
	}

	ipTCPOverhead := 40
	if tcpAddr.IP.To4() == nil {
		ipTCPOverhead = 60
	}

	mss := mtu - ipTCPOverhead
	if mss <= 0 {
		return obfs.TcpMss
	}

	if mss < 536 {
		return 536
	}
	if mss > obfs.TcpMss {
		return obfs.TcpMss
	}
	return mss
}

func detectInterfaceMTUByIP(ip net.IP) int {
	if ip == nil {
		return 0
	}
	now := time.Now()
	key := ip.String()
	if cached, ok := mtuByIPCache.Load(key); ok {
		switch v := cached.(type) {
		case mtuCacheEntry:
			if now.UnixNano() < v.expiresAt {
				return v.mtu
			}
		case int:
			return v
		}
	}
	mtu := detectInterfaceMTUByIPSlowFunc(ip)
	mtuByIPCache.Store(key, mtuCacheEntry{mtu: mtu, expiresAt: now.Add(mtuCacheTTL).UnixNano()})
	return mtu
}

var mtuByIPCache sync.Map
var mtuCacheTTL = 5 * time.Minute
var detectInterfaceMTUByIPSlowFunc = detectInterfaceMTUByIPSlow

type mtuCacheEntry struct {
	mtu       int
	expiresAt int64
}

func detectInterfaceMTUByIPSlow(ip net.IP) int {
	interfaces, err := net.Interfaces()
	if err != nil {
		return 0
	}

	for _, iface := range interfaces {
		if iface.MTU <= 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet == nil {
				continue
			}
			if ipNet.Contains(ip) {
				return iface.MTU
			}
		}
	}

	return 0
}

func (ssrd *ShadowsocksRDecorate) UpdateUser(uid []byte) {
	if ssrd.single == 1 {
		uidInt := binaryx.LEBytesToUInt32(uid)
		ssrd.UID = int(uidInt)
		log.Infow("ShadowsocksRDecorate update uid", log.FieldUID, uidInt)
	}
}
