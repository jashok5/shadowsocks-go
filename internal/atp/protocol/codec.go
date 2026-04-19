package protocol

import (
	"errors"
	"io"
)

var ErrPayloadTooLarge = errors.New("payload too large")

func ReadFrame(r io.Reader) (*Frame, error) {
	headerBuf := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	var h Header
	if err := h.UnmarshalBinary(headerBuf); err != nil {
		return nil, err
	}

	payload := make([]byte, int(h.Length))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	return &Frame{Header: h, Payload: payload}, nil
}

func WriteFrame(w io.Writer, f *Frame) error {
	if len(f.Payload) > 0xFFFF {
		return ErrPayloadTooLarge
	}

	h := f.Header
	h.Length = uint16(len(f.Payload))
	hb, err := h.MarshalBinary()
	if err != nil {
		return err
	}

	if _, err := w.Write(hb); err != nil {
		return err
	}
	if len(f.Payload) == 0 {
		return nil
	}
	_, err = w.Write(f.Payload)
	return err
}
