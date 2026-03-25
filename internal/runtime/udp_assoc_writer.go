package runtime

import (
	"context"
	"fmt"
	"net"

	"golang.org/x/time/rate"
)

type udpWriteConn interface {
	Write([]byte) (int, error)
}

func writeUDPToConn(ctx context.Context, limiter *rate.Limiter, conn udpWriteConn, payload []byte) error {
	if conn == nil {
		return fmt.Errorf("udp writer is nil")
	}
	if err := waitLimiter(ctx, limiter, len(payload)); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func writeUDPToPacketConn(ctx context.Context, limiter *rate.Limiter, pc net.PacketConn, payload []byte, target net.Addr) error {
	if pc == nil {
		return fmt.Errorf("udp packet conn is nil")
	}
	if target == nil {
		return fmt.Errorf("udp target addr is nil")
	}
	if err := waitLimiter(ctx, limiter, len(payload)); err != nil {
		return err
	}
	_, err := pc.WriteTo(payload, target)
	return err
}
