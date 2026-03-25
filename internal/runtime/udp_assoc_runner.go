package runtime

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

type udpAssocRunnerSnapshot struct {
	ActiveReaders int64
	Packets       int64
	Errors        int64
}

type udpAssocAlertSnapshot struct {
	ErrorDelta int64
	Packets    int64
	Errors     int64
	Warn       bool
}

type udpAssocRunnerMetrics struct {
	activeReaders atomic.Int64
	packets       atomic.Int64
	errors        atomic.Int64
	lastAlertErr  atomic.Int64
}

func (m *udpAssocRunnerMetrics) Snapshot() udpAssocRunnerSnapshot {
	if m == nil {
		return udpAssocRunnerSnapshot{}
	}
	return udpAssocRunnerSnapshot{
		ActiveReaders: m.activeReaders.Load(),
		Packets:       m.packets.Load(),
		Errors:        m.errors.Load(),
	}
}

func (m *udpAssocRunnerMetrics) AlertSnapshot(errorDeltaThreshold int64) udpAssocAlertSnapshot {
	if m == nil {
		return udpAssocAlertSnapshot{}
	}
	if errorDeltaThreshold <= 0 {
		errorDeltaThreshold = 1
	}
	curErr := m.errors.Load()
	prevErr := m.lastAlertErr.Swap(curErr)
	delta := curErr - prevErr
	return udpAssocAlertSnapshot{
		ErrorDelta: delta,
		Packets:    m.packets.Load(),
		Errors:     curErr,
		Warn:       delta >= errorDeltaThreshold,
	}
}

type udpAssocRunnerConfig struct {
	Ctx        context.Context
	Spawn      func(func())
	Timeout    time.Duration
	BufferSize int

	PacketConn func() net.PacketConn
	OnPacket   func(payload []byte, source net.Addr)
	OnError    func(error, udpAssocErrorKind)
	OnExit     func()
	Metrics    *udpAssocRunnerMetrics
}

func startUDPAssocReader(cfg udpAssocRunnerConfig) {
	if cfg.Spawn == nil || cfg.PacketConn == nil || cfg.OnPacket == nil {
		return
	}
	bufSize := cfg.BufferSize
	if bufSize <= 0 {
		bufSize = udpBufSize
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = udpSessionReadTimeout
	}

	cfg.Spawn(func() {
		if cfg.Metrics != nil {
			cfg.Metrics.activeReaders.Add(1)
			defer cfg.Metrics.activeReaders.Add(-1)
		}
		if cfg.OnExit != nil {
			defer cfg.OnExit()
		}
		buf := acquireUDPPacketBuf(bufSize)
		defer releaseUDPPacketBuf(buf)
		for {
			pc := cfg.PacketConn()
			if pc == nil {
				return
			}
			_ = pc.SetReadDeadline(time.Now().Add(timeout))
			n, src, err := pc.ReadFrom(buf)
			if n > 0 {
				if cfg.Metrics != nil {
					cfg.Metrics.packets.Add(1)
				}
				pkt := acquireUDPPacketBuf(n)
				copy(pkt, buf[:n])
				cfg.OnPacket(pkt[:n], src)
				releaseUDPPacketBuf(pkt)
			}
			if err != nil {
				kind := classifyUDPAssocError(cfg.Ctx, err)
				if kind == udpAssocErrTimeout {
					select {
					case <-cfg.Ctx.Done():
						return
					default:
						continue
					}
				}
				if cfg.OnError != nil {
					if cfg.Metrics != nil {
						cfg.Metrics.errors.Add(1)
					}
					cfg.OnError(err, kind)
				}
				return
			}
		}
	})
}
