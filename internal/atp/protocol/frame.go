package protocol

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	Magic      uint16 = 0x4154
	VersionV1  uint8  = 1
	HeaderSize        = 26

	TypeHello       uint8 = 0x01
	TypeAuth        uint8 = 0x02
	TypeOpenStream  uint8 = 0x03
	TypeData        uint8 = 0x04
	TypeWindowUp    uint8 = 0x05
	TypePing        uint8 = 0x06
	TypeCloseStream uint8 = 0x07
	TypeCloseSess   uint8 = 0x08
	TypeError       uint8 = 0x09
	TypeDatagram    uint8 = 0x0A
)

var (
	ErrInvalidHeaderSize = errors.New("invalid header size")
	ErrBadMagic          = errors.New("bad magic")
	ErrBadHeaderCRC      = errors.New("bad header crc")
)

type Header struct {
	Magic     uint16
	Version   uint8
	Type      uint8
	Flags     uint8
	Reserved  uint8
	SessionID uint64
	StreamID  uint32
	Seq       uint32
	Length    uint16
	HeaderCRC uint16
}

type Frame struct {
	Header  Header
	Payload []byte
}

func (h Header) MarshalBinary() ([]byte, error) {
	b := make([]byte, HeaderSize)
	binary.BigEndian.PutUint16(b[0:2], h.Magic)
	b[2] = h.Version
	b[3] = h.Type
	b[4] = h.Flags
	b[5] = h.Reserved
	binary.BigEndian.PutUint64(b[6:14], h.SessionID)
	binary.BigEndian.PutUint32(b[14:18], h.StreamID)
	binary.BigEndian.PutUint32(b[18:22], h.Seq)
	binary.BigEndian.PutUint16(b[22:24], h.Length)
	crc := headerCRC(b[:24])
	binary.BigEndian.PutUint16(b[24:26], crc)
	return b, nil
}

func (h *Header) UnmarshalBinary(b []byte) error {
	if len(b) != HeaderSize {
		return ErrInvalidHeaderSize
	}
	h.Magic = binary.BigEndian.Uint16(b[0:2])
	if h.Magic != Magic {
		return ErrBadMagic
	}
	h.Version = b[2]
	h.Type = b[3]
	h.Flags = b[4]
	h.Reserved = b[5]
	h.SessionID = binary.BigEndian.Uint64(b[6:14])
	h.StreamID = binary.BigEndian.Uint32(b[14:18])
	h.Seq = binary.BigEndian.Uint32(b[18:22])
	h.Length = binary.BigEndian.Uint16(b[22:24])
	h.HeaderCRC = binary.BigEndian.Uint16(b[24:26])
	if h.HeaderCRC != headerCRC(b[:24]) {
		return ErrBadHeaderCRC
	}
	return nil
}

func headerCRC(headerWithoutCRC []byte) uint16 {
	sum := crc32.ChecksumIEEE(headerWithoutCRC)
	return uint16(sum & 0xFFFF)
}
