package bucket

import "errors"

var (
	ErrBufferTooSmall    = errors.New("buffer too small")
	ErrPacketTooOld      = errors.New("received packet too old")
	ErrPacketTooNew      = errors.New("received packet too new")
	ErrRTXPacket         = errors.New("packet already received")
	ErrPacketMismatch    = errors.New("sequence number mismatch")
	ErrPacketSizeInvalid = errors.New("invalid size")
)
