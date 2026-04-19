package recovery

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidTicket   = errors.New("invalid ticket")
	ErrExpiredTicket   = errors.New("expired ticket")
	ErrReplayTicket    = errors.New("replayed ticket")
	ErrBindingMismatch = errors.New("ticket binding mismatch")
)

type Config struct {
	TTL time.Duration
}

func (c Config) withDefaults() Config {
	if c.TTL <= 0 {
		c.TTL = 15 * time.Minute
	}
	return c
}

type Manager struct {
	mu     sync.Mutex
	secret []byte
	used   map[string]time.Time
	config Config
	now    func() time.Time
}

func NewManager(config Config) (*Manager, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return NewManagerWithSecret(config, secret), nil
}

func NewManagerWithSecret(config Config, secret []byte) *Manager {
	config = config.withDefaults()
	sec := make([]byte, len(secret))
	copy(sec, secret)
	return &Manager{
		secret: sec,
		used:   make(map[string]time.Time),
		config: config,
		now:    time.Now,
	}
}

func (m *Manager) Issue(sessionID uint64, binding string) (string, error) {
	issuedAt := m.now().Unix()
	expiresAt := m.now().Add(m.config.TTL).Unix()
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	body := encodeBody(sessionID, issuedAt, expiresAt, binding, nonce)
	sig := m.sign(body)
	b := append(body, sig...)
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) Consume(ticket string, expectedBinding string) (uint64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil {
		return 0, ErrInvalidTicket
	}
	if len(raw) < 8+8+8+2+12+32 {
		return 0, ErrInvalidTicket
	}
	body := raw[:len(raw)-32]
	sig := raw[len(raw)-32:]
	if !hmac.Equal(sig, m.sign(body)) {
		return 0, ErrInvalidTicket
	}

	sessionID, expiresAt, binding, err := decodeBody(body)
	if err != nil {
		return 0, ErrInvalidTicket
	}
	if expectedBinding != "" && binding != expectedBinding {
		return 0, ErrBindingMismatch
	}
	now := m.now()
	if now.Unix() > expiresAt {
		return 0, ErrExpiredTicket
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.gcUsedLocked(now)
	if _, ok := m.used[ticket]; ok {
		return 0, ErrReplayTicket
	}
	m.used[ticket] = time.Unix(expiresAt, 0)
	return sessionID, nil
}

func (m *Manager) sign(b []byte) []byte {
	h := hmac.New(sha256.New, m.secret)
	_, _ = h.Write(b)
	return h.Sum(nil)
}

func encodeBody(sessionID uint64, issuedAt int64, expiresAt int64, binding string, nonce []byte) []byte {
	bindingBytes := []byte(binding)
	b := make([]byte, 0, 8+8+8+2+len(bindingBytes)+len(nonce))
	tmp := make([]byte, 8)
	binary.BigEndian.PutUint64(tmp, sessionID)
	b = append(b, tmp...)
	binary.BigEndian.PutUint64(tmp, uint64(issuedAt))
	b = append(b, tmp...)
	binary.BigEndian.PutUint64(tmp, uint64(expiresAt))
	b = append(b, tmp...)
	bl := make([]byte, 2)
	binary.BigEndian.PutUint16(bl, uint16(len(bindingBytes)))
	b = append(b, bl...)
	b = append(b, bindingBytes...)
	b = append(b, nonce...)
	return b
}

func decodeBody(body []byte) (sessionID uint64, expiresAt int64, binding string, err error) {
	if len(body) < 26 {
		return 0, 0, "", ErrInvalidTicket
	}
	sessionID = binary.BigEndian.Uint64(body[0:8])
	expiresAt = int64(binary.BigEndian.Uint64(body[16:24]))
	bl := int(binary.BigEndian.Uint16(body[24:26]))
	if len(body) < 26+bl+12 {
		return 0, 0, "", ErrInvalidTicket
	}
	binding = string(body[26 : 26+bl])
	return sessionID, expiresAt, binding, nil
}

func (m *Manager) gcUsedLocked(now time.Time) {
	for t, exp := range m.used {
		if now.After(exp) {
			delete(m.used, t)
		}
	}
}
