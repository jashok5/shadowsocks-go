package core

import (
	"errors"
	"net"
)

func ListenPacket(network, address string, ciph PacketConnCipher) (net.PacketConn, error) {
	c, err := net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	wrapped := ciph.PacketConn(c)
	if wrapped == nil {
		_ = c.Close()
		return nil, errors.New("packet cipher returned nil PacketConn")
	}
	return wrapped, nil
}
