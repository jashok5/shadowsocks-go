package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	TLVOpenNetwork uint16 = 0x0101
	TLVOpenHost    uint16 = 0x0102
	TLVOpenPort    uint16 = 0x0103
	TLVOpenDomain  uint16 = 0x0104
)

const (
	NetworkTCP uint8 = 1
	NetworkUDP uint8 = 2
)

var ErrInvalidControlPayload = errors.New("invalid control payload")

type OpenStreamRequest struct {
	Network uint8
	Host    string
	Port    uint16
	Domain  string
}

func EncodeOpenStreamPayload(req OpenStreamRequest) ([]byte, error) {
	if req.Network != NetworkTCP && req.Network != NetworkUDP {
		return nil, fmt.Errorf("%w: unknown network=%d", ErrInvalidControlPayload, req.Network)
	}
	if req.Host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidControlPayload)
	}
	if req.Port == 0 {
		return nil, fmt.Errorf("%w: missing port", ErrInvalidControlPayload)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, req.Port)
	tlvs := []TLV{
		{Type: TLVOpenNetwork, Value: []byte{req.Network}},
		{Type: TLVOpenHost, Value: []byte(req.Host)},
		{Type: TLVOpenPort, Value: portBytes},
	}
	if req.Domain != "" {
		tlvs = append(tlvs, TLV{Type: TLVOpenDomain, Value: []byte(req.Domain)})
	}
	return EncodeTLVs(tlvs)
}

func DecodeOpenStreamPayload(payload []byte) (OpenStreamRequest, error) {
	items, err := DecodeTLVs(payload)
	if err != nil {
		return OpenStreamRequest{}, err
	}
	var req OpenStreamRequest
	for _, it := range items {
		switch it.Type {
		case TLVOpenNetwork:
			if len(it.Value) != 1 {
				return OpenStreamRequest{}, fmt.Errorf("%w: invalid network length", ErrInvalidControlPayload)
			}
			req.Network = it.Value[0]
		case TLVOpenHost:
			req.Host = string(it.Value)
		case TLVOpenPort:
			if len(it.Value) != 2 {
				return OpenStreamRequest{}, fmt.Errorf("%w: invalid port length", ErrInvalidControlPayload)
			}
			req.Port = binary.BigEndian.Uint16(it.Value)
		case TLVOpenDomain:
			req.Domain = string(it.Value)
		}
	}
	if req.Network != NetworkTCP && req.Network != NetworkUDP {
		return OpenStreamRequest{}, fmt.Errorf("%w: unknown network=%d", ErrInvalidControlPayload, req.Network)
	}
	if req.Host == "" {
		return OpenStreamRequest{}, fmt.Errorf("%w: missing host", ErrInvalidControlPayload)
	}
	if req.Port == 0 {
		return OpenStreamRequest{}, fmt.Errorf("%w: missing port", ErrInvalidControlPayload)
	}
	return req, nil
}
