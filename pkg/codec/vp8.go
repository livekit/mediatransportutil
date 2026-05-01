// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package codec

import (
	"encoding/binary"
	"errors"
)

var (
	errShortPacket   = errors.New("packet is not large enough")
	errNilPacket     = errors.New("invalid nil packet")
	errInvalidPacket = errors.New("invalid packet")
)

// VP8 is a helper to get temporal data from VP8 packet header
/*
	VP8 Payload Descriptor
			0 1 2 3 4 5 6 7                      0 1 2 3 4 5 6 7
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
			|X|R|N|S|R| PID | (REQUIRED)        |X|R|N|S|R| PID | (REQUIRED)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		X:  |I|L|T|K| RSV   | (OPTIONAL)   X:   |I|L|T|K| RSV   | (OPTIONAL)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		I:  |M| PictureID   | (OPTIONAL)   I:   |M| PictureID   | (OPTIONAL)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		L:  |   TL0PICIDX   | (OPTIONAL)        |   PictureID   |
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		T/K:|TID|Y| KEYIDX  | (OPTIONAL)   L:   |   TL0PICIDX   | (OPTIONAL)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		T/K:|TID|Y| KEYIDX  | (OPTIONAL)
			+-+-+-+-+-+-+-+-+
*/
type VP8 struct {
	FirstByte byte
	S         bool

	I         bool
	M         bool
	PictureID uint16 /* 7 or 15 bits, picture ID */

	L         bool
	TL0PICIDX uint8 /* 8 bits temporal level zero index */

	// Optional Header If either of the T or K bits are set to 1,
	// the TID/Y/KEYIDX extension field MUST be present.
	T   bool
	TID uint8 /* 2 bits temporal layer idx */
	Y   bool

	K      bool
	KEYIDX uint8 /* 5 bits of key frame idx */

	HeaderSize int

	// IsKeyFrame is a helper to detect if current packet is a keyframe
	IsKeyFrame bool
}

// Unmarshal parses the passed byte slice and stores the result in the VP8 this method is called upon
func (v *VP8) Unmarshal(payload []byte) error {
	if payload == nil {
		return errNilPacket
	}

	payloadLen := len(payload)
	if payloadLen < 1 {
		return errShortPacket
	}

	idx := 0
	v.FirstByte = payload[idx]
	v.S = payload[idx]&0x10 > 0
	// Check for extended bit control
	if payload[idx]&0x80 > 0 {
		idx++
		if payloadLen < idx+1 {
			return errShortPacket
		}
		v.I = payload[idx]&0x80 > 0
		v.L = payload[idx]&0x40 > 0
		v.T = payload[idx]&0x20 > 0
		v.K = payload[idx]&0x10 > 0
		if v.L && !v.T {
			return errInvalidPacket
		}

		if v.I {
			idx++
			if payloadLen < idx+1 {
				return errShortPacket
			}
			pid := payload[idx] & 0x7f
			// if m is 1, then Picture ID is 15 bits
			v.M = payload[idx]&0x80 > 0
			if v.M {
				idx++
				if payloadLen < idx+1 {
					return errShortPacket
				}
				v.PictureID = binary.BigEndian.Uint16([]byte{pid, payload[idx]})
			} else {
				v.PictureID = uint16(pid)
			}
		}

		if v.L {
			idx++
			if payloadLen < idx+1 {
				return errShortPacket
			}
			v.TL0PICIDX = payload[idx]
		}

		if v.T || v.K {
			idx++
			if payloadLen < idx+1 {
				return errShortPacket
			}

			if v.T {
				v.TID = (payload[idx] & 0xc0) >> 6
				v.Y = (payload[idx] & 0x20) > 0
			}

			if v.K {
				v.KEYIDX = payload[idx] & 0x1f
			}
		}
		idx++
		if payloadLen < idx+1 {
			return errShortPacket
		}

		// Check is packet is a keyframe by looking at P bit in vp8 payload
		v.IsKeyFrame = payload[idx]&0x01 == 0 && v.S
	} else {
		idx++
		if payloadLen < idx+1 {
			return errShortPacket
		}
		// Check is packet is a keyframe by looking at P bit in vp8 payload
		v.IsKeyFrame = payload[idx]&0x01 == 0 && v.S
	}
	v.HeaderSize = idx
	return nil
}

func (v VP8) Marshal() ([]byte, error) {
	var buf [8]byte
	n, err := v.MarshalTo(buf[:])
	if err != nil {
		return nil, err
	}
	return buf[:n], err
}

func (v VP8) MarshalTo(buf []byte) (int, error) {
	if len(buf) < v.HeaderSize {
		return 0, errShortPacket
	}

	idx := 0
	buf[idx] = v.FirstByte
	if v.I || v.L || v.T || v.K {
		buf[idx] |= 0x80 // X bit
		idx++

		xpos := idx
		xval := byte(0)

		idx++
		if v.I {
			xval |= (1 << 7)
			if v.M {
				buf[idx] = 0x80 | byte((v.PictureID>>8)&0x7f)
				buf[idx+1] = byte(v.PictureID & 0xff)
				idx += 2
			} else {
				buf[idx] = byte(v.PictureID)
				idx++
			}
		}

		if v.L {
			xval |= (1 << 6)
			buf[idx] = v.TL0PICIDX
			idx++
		}

		if v.T || v.K {
			buf[idx] = 0
			if v.T {
				xval |= (1 << 5)
				buf[idx] = v.TID << 6
				if v.Y {
					buf[idx] |= (1 << 5)
				}
			}

			if v.K {
				xval |= (1 << 4)
				buf[idx] |= v.KEYIDX & 0x1f
			}
			idx++
		}

		buf[xpos] = xval
	} else {
		buf[idx] &^= 0x80 // X bit
		idx++
	}

	return idx, nil
}

// -------------------------------------

// ExtractVP8VideoSize extracts video resolution from VP8 key frame
func ExtractVP8VideoSize(vp8Packet *VP8, payload []byte) VideoSize {
	if !vp8Packet.IsKeyFrame || len(payload) < vp8Packet.HeaderSize+10 {
		return VideoSize{}
	}

	vp8Payload := payload[vp8Packet.HeaderSize:]

	// Check for VP8 start code
	if len(vp8Payload) < 10 || vp8Payload[3] != 0x9D || vp8Payload[4] != 0x01 || vp8Payload[5] != 0x2A {
		return VideoSize{}
	}

	// Read width and height from bytes 6-9
	width := uint32(vp8Payload[6]) | (uint32(vp8Payload[7]) << 8)
	height := uint32(vp8Payload[8]) | (uint32(vp8Payload[9]) << 8)

	return VideoSize{width & 0x3FFF, height & 0x3FFF}
}
