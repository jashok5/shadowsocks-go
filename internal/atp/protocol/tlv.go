package protocol

import (
	"encoding/binary"
	"errors"
)

var (
	ErrInvalidTLV = errors.New("invalid tlv")
)

type TLV struct {
	Type  uint16
	Value []byte
}

func EncodeTLVs(items []TLV) ([]byte, error) {
	total := 0
	for _, it := range items {
		if len(it.Value) > 0xFFFF {
			return nil, ErrInvalidTLV
		}
		total += 4 + len(it.Value)
	}
	out := make([]byte, total)
	off := 0
	for _, it := range items {
		binary.BigEndian.PutUint16(out[off:off+2], it.Type)
		binary.BigEndian.PutUint16(out[off+2:off+4], uint16(len(it.Value)))
		copy(out[off+4:off+4+len(it.Value)], it.Value)
		off += 4 + len(it.Value)
	}
	return out, nil
}

func DecodeTLVs(b []byte) ([]TLV, error) {
	items := make([]TLV, 0, 4)
	off := 0
	for off < len(b) {
		if len(b)-off < 4 {
			return nil, ErrInvalidTLV
		}
		t := binary.BigEndian.Uint16(b[off : off+2])
		l := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		off += 4
		if l < 0 || off+l > len(b) {
			return nil, ErrInvalidTLV
		}
		v := make([]byte, l)
		copy(v, b[off:off+l])
		off += l
		items = append(items, TLV{Type: t, Value: v})
	}
	return items, nil
}
