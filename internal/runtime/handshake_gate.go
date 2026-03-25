package runtime

import "context"

type HandshakeAcquireResult int

const (
	handshakeAcquireOK HandshakeAcquireResult = iota
	handshakeAcquireBlocked
	handshakeAcquirePerIPLimit
	handshakeAcquireGlobalLimit
	handshakeAcquireCanceled
)

type handshakeGate struct {
	auth *authFailGuard
	sem  chan struct{}
}

type handshakeLease struct {
	gate         *handshakeGate
	handshakeKey string
	semHeld      bool
	released     bool
}

func newHandshakeGate(auth *authFailGuard, capLimit int) *handshakeGate {
	if capLimit <= 0 {
		capLimit = defaultHandshakeMaxConcurrent
	}
	return &handshakeGate{auth: auth, sem: make(chan struct{}, capLimit)}
}

func (g *handshakeGate) Acquire(ctx context.Context, ip string, port int) (handshakeLease, HandshakeAcquireResult) {
	lease := handshakeLease{gate: g}
	if g == nil {
		return lease, handshakeAcquireOK
	}
	if authShouldBlock(g.auth, ip) {
		return lease, handshakeAcquireBlocked
	}
	key := authHandshakeKey(ip, port)
	lease.handshakeKey = key
	if !authTryAcquireHandshake(g.auth, key) {
		return lease, handshakeAcquirePerIPLimit
	}
	select {
	case <-ctx.Done():
		authReleaseHandshake(g.auth, key)
		return handshakeLease{}, handshakeAcquireCanceled
	case g.sem <- struct{}{}:
		lease.semHeld = true
		return lease, handshakeAcquireOK
	default:
		authReleaseHandshake(g.auth, key)
		return handshakeLease{}, handshakeAcquireGlobalLimit
	}
}

func (l *handshakeLease) Release() {
	if l == nil || l.released || l.gate == nil {
		return
	}
	l.released = true
	if l.semHeld {
		select {
		case <-l.gate.sem:
		default:
		}
	}
	authReleaseHandshake(l.gate.auth, l.handshakeKey)
}
