package addrx

import (
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/langx"

	"net"
	"strconv"
)

func GetIPFromAddr(addr net.Addr) string {
	switch addr.(type) {
	case *net.TCPAddr:
		tcpAddr := addr.(*net.TCPAddr)
		return tcpAddr.IP.String()
	case *net.UDPAddr:
		udpAddr := addr.(*net.UDPAddr)
		return udpAddr.IP.String()
	case nil:
		return ""
	default:
		return ""
	}
}

func GetPortFromAddr(addr net.Addr) int {
	switch addr.(type) {
	case *net.TCPAddr:
		tcpAddr := addr.(*net.TCPAddr)
		return tcpAddr.Port
	case *net.UDPAddr:
		udpAddr := addr.(*net.UDPAddr)
		return udpAddr.Port
	case nil:
		return 0
	default:
		return 0
	}
}

func SplitPortFromAddr(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	return langx.FirstResult(strconv.Atoi, port).(int)
}
