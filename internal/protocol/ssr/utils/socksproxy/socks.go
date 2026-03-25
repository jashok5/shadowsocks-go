package socksproxy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/pkg/errors"
)

const (
	AtypIPv4       = 1
	AtypDomainName = 3
	AtypIPv6       = 4
)

type Error string

func (err Error) Error() string {
	return "SOCKS error: " + string(err)
}

const (
	ErrAddressNotSupported = Error("ErrAddressNotSupported")
)

const MaxAddrLen = 1 + 1 + 255 + 2

type Socks5Addr struct {
	Raw     []byte
	AType   int
	Address string
	Port    int
}

func NewSocks5Addr(raw []byte, atype int) *Socks5Addr {
	addr := &Socks5Addr{
		Raw:   raw,
		AType: atype,
	}
	addr.process()
	return addr
}

func (ss *Socks5Addr) GetRaw() (raw []byte, err error) {
	if ss.Raw != nil {
		return ss.Raw, nil
	}

	buf := new(bytes.Buffer)
	var data []any
	switch ss.AType {
	case AtypDomainName:
		domainLen := len(ss.Address)
		data = []any{
			uint8(ss.AType),
			uint8(domainLen),
			[]byte(ss.Address),
			uint16(ss.Port),
		}
	case AtypIPv4:
		data = []any{
			uint8(ss.AType),
			[]byte(net.ParseIP(ss.Address).To4()),
			uint16(ss.Port),
		}
	case AtypIPv6:
		data = []any{
			uint8(ss.AType),
			[]byte(net.ParseIP(ss.Address).To16()),
			uint16(ss.Port),
		}
	}
	for _, v := range data {
		err := binary.Write(buf, binary.BigEndian, v)
		if err != nil {
			return nil, errors.Wrap(errors.WithStack(err), "GetRaw is errors")
		}
	}
	raw = make([]byte, buf.Len())
	copy(raw, buf.Bytes())
	ss.Raw = raw
	return raw, nil
}

func (ss *Socks5Addr) MustGetRaw() []byte {
	raw, err := ss.GetRaw()
	if err != nil {
		return nil
	}
	return raw
}

func (ss *Socks5Addr) GetAddress() string {
	return ss.Address
}

func (ss *Socks5Addr) GetPort() int {
	return ss.Port
}

func (ss *Socks5Addr) GetAType() int {
	return ss.AType
}

func (ss *Socks5Addr) process() {
	switch ss.AType {
	case AtypDomainName:
		ss.Address = string(ss.Raw[2 : 2+int(ss.Raw[1])])
		ss.Port = (int(ss.Raw[2+int(ss.Raw[1])]) << 8) | int(ss.Raw[2+int(ss.Raw[1])+1])
	case AtypIPv4:
		ss.Address = net.IP(ss.Raw[1 : 1+net.IPv4len]).String()
		ss.Port = (int(ss.Raw[1+net.IPv4len]) << 8) | int(ss.Raw[1+net.IPv4len+1])
	case AtypIPv6:
		ss.Address = net.IP(ss.Raw[1 : 1+net.IPv6len]).String()
		ss.Port = (int(ss.Raw[1+net.IPv6len]) << 8) | int(ss.Raw[1+net.IPv6len+1])
	}
}

func (ss *Socks5Addr) String() string {
	return fmt.Sprintf("%s:%v", ss.Address, ss.Port)
}

func readAddr(r io.Reader, b []byte) (*Socks5Addr, error) {
	if len(b) < MaxAddrLen {
		return nil, io.ErrShortBuffer
	}
	_, err := io.ReadFull(r, b[:1])
	if err != nil {
		return nil, err
	}

	switch b[0] {
	case AtypDomainName:
		_, err = io.ReadFull(r, b[1:2])
		if err != nil {
			return nil, err
		}
		_, err = io.ReadFull(r, b[2:2+int(b[1])+2])
		return NewSocks5Addr(b[:1+1+int(b[1])+2], AtypDomainName), err
	case AtypIPv4:
		_, err = io.ReadFull(r, b[1:1+net.IPv4len+2])
		return NewSocks5Addr(b[:1+net.IPv4len+2], AtypIPv4), err
	case AtypIPv6:
		_, err = io.ReadFull(r, b[1:1+net.IPv6len+2])
		return NewSocks5Addr(b[:1+net.IPv6len+2], AtypIPv6), err
	}

	return nil, ErrAddressNotSupported
}

func ReadAddr(r io.Reader) (*Socks5Addr, error) {
	return readAddr(r, make([]byte, MaxAddrLen))
}

func SplitAddr(b []byte) (*Socks5Addr, error) {
	addrLen := 1
	if len(b) < addrLen {
		return nil, io.ErrShortBuffer
	}

	var atype int
	switch b[0] {
	case AtypDomainName:
		if len(b) < 2 {
			return nil, io.ErrShortBuffer
		}
		addrLen = 1 + 1 + int(b[1]) + 2
		atype = AtypDomainName
	case AtypIPv4:
		addrLen = 1 + net.IPv4len + 2
		atype = AtypIPv4
	case AtypIPv6:
		addrLen = 1 + net.IPv6len + 2
		atype = AtypIPv6
	default:
		return nil, errors.New("atype error")

	}

	if len(b) < addrLen {
		return nil, io.ErrShortBuffer
	}

	return NewSocks5Addr(b[:addrLen], atype), nil
}

func ParseAddr(s string) *Socks5Addr {
	var (
		addr  []byte
		aType int
	)
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			addr = make([]byte, 1+net.IPv4len+2)
			addr[0] = AtypIPv4
			copy(addr[1:], ip4)
			aType = AtypIPv4
		} else {
			addr = make([]byte, 1+net.IPv6len+2)
			addr[0] = AtypIPv6
			copy(addr[1:], ip)
			aType = AtypIPv6
		}
	} else {
		if len(host) > 255 {
			return nil
		}
		addr = make([]byte, 1+1+len(host)+2)
		addr[0] = AtypDomainName
		addr[1] = byte(len(host))
		copy(addr[2:], host)
		aType = AtypDomainName
	}

	portnum, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil
	}

	addr[len(addr)-2], addr[len(addr)-1] = byte(portnum>>8), byte(portnum)

	return NewSocks5Addr(addr, aType)
}
