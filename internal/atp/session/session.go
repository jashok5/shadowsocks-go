package session

import (
	"crypto/rand"
	"encoding/binary"

	"sync/atomic"
)

func NewSessionID() (uint64, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

type Sequencer struct {
	value atomic.Uint32
}

func NewSequencer(start uint32) *Sequencer {
	s := &Sequencer{}
	s.value.Store(start)
	return s
}

func (s *Sequencer) Next() uint32 {
	return s.value.Add(1)
}
