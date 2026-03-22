package core

import (
	"errors"
	"net"
)

type listener struct {
	net.Listener
	StreamConnCipher
}

func Listen(network, address string, ciph StreamConnCipher) (net.Listener, error) {
	l, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &listener{l, ciph}, nil
}

func (l *listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := l.StreamConn(c)
	if wrapped == nil {
		_ = c.Close()
		return nil, errors.New("stream cipher returned nil Conn")
	}
	return wrapped, nil
}
