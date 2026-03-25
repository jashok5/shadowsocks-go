package runtime

import (
	"context"
	"errors"
	"net"
	"strings"
)

type udpAssocErrorKind int

const (
	udpAssocErrNone udpAssocErrorKind = iota
	udpAssocErrTimeout
	udpAssocErrCanceled
	udpAssocErrClosed
	udpAssocErrIO
)

func (k udpAssocErrorKind) String() string {
	switch k {
	case udpAssocErrNone:
		return "none"
	case udpAssocErrTimeout:
		return "timeout"
	case udpAssocErrCanceled:
		return "canceled"
	case udpAssocErrClosed:
		return "closed"
	case udpAssocErrIO:
		return "io"
	default:
		return "unknown"
	}
}

func classifyUDPAssocError(ctx context.Context, err error) udpAssocErrorKind {
	if err == nil {
		return udpAssocErrNone
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return udpAssocErrCanceled
	}
	if ctx != nil && ctx.Err() != nil {
		return udpAssocErrCanceled
	}
	if errors.Is(err, net.ErrClosed) {
		return udpAssocErrClosed
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return udpAssocErrTimeout
	}
	if strings.Contains(strings.ToLower(err.Error()), "closed network connection") {
		return udpAssocErrClosed
	}
	return udpAssocErrIO
}
