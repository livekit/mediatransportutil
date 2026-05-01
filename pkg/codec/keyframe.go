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
	"strings"

	"github.com/pion/rtp/codecs"

	"github.com/livekit/protocol/logger"
)

func IsKeyFrame(codec string, payload []byte) bool {
	switch strings.ToLower(codec) {
	case "h264":
		return IsH264KeyFrame(payload)
	case "h265":
		return IsH265KeyFrame(payload)
	case "vp8":
		return IsVP8KeyFrame(payload)
	case "vp9":
		return IsVP9KeyFrame(nil, payload)
	case "av1":
		return IsAV1KeyFrame(payload)
	}

	return false

}

// IsH264KeyFrame detects if h264 payload is a keyframe
// this code was taken from https://github.com/jech/galene/blob/codecs/rtpconn/rtpreader.go#L45
// all credits belongs to Juliusz Chroboczek @jech and the awesome Galene SFU
func IsH264KeyFrame(payload []byte) bool {
	if len(payload) < 1 {
		return false
	}
	nalu := payload[0] & 0x1F
	if nalu == 0 {
		// reserved
		return false
	} else if nalu <= 23 {
		// simple NALU
		return nalu == 7
	} else if nalu == 24 || nalu == 25 || nalu == 26 || nalu == 27 {
		// STAP-A, STAP-B, MTAP16 or MTAP24
		i := 1
		if nalu == 25 || nalu == 26 || nalu == 27 {
			// skip DON
			i += 2
		}
		for i < len(payload) {
			if i+2 > len(payload) {
				return false
			}
			length := uint16(payload[i])<<8 |
				uint16(payload[i+1])
			i += 2
			if i+int(length) > len(payload) {
				return false
			}
			offset := 0
			switch nalu {
			case 26:
				offset = 3
			case 27:
				offset = 4
			}
			if offset >= int(length) {
				return false
			}
			n := payload[i+offset] & 0x1F
			if n == 7 {
				return true
			} else if n >= 24 {
				// is this legal?
				logger.Debugw("Non-simple NALU within a STAP")
			}
			i += int(length)
		}
		if i == len(payload) {
			return false
		}
		return false
	} else if nalu == 28 || nalu == 29 {
		// FU-A or FU-B
		if len(payload) < 2 {
			return false
		}
		if (payload[1] & 0x80) == 0 {
			// not a starting fragment
			return false
		}
		return payload[1]&0x1F == 7
	}
	return false
}

func IsH265KeyFrame(payload []byte) (kf bool) {
	if len(payload) < 2 {
		return false
	}
	naluType := (payload[0] & 0x7E) >> 1
	switch naluType {
	case 33, 34:
		return true
	case 48: // AP
		idx := 2
		for idx < len(payload)-2 {
			// TODO: check the DONL field (controlled by sprop-max-don-diff)
			size := binary.BigEndian.Uint16(payload[idx:])
			idx += 2
			if idx >= len(payload) {
				return false
			}
			naluType = (payload[idx] & 0x7E) >> 1
			if naluType == 33 || naluType == 34 {
				return true
			}
			idx += int(size)
		}
		return false

	case 49: // FU
		if len(payload) < 3 {
			return false
		}
		naluType = (payload[2] & 0x7E) >> 1
		return naluType == 33 || naluType == 34
	default:
		return false
	}
}

func IsVP8KeyFrame(payload []byte) bool {
	var vp8 VP8
	if err := vp8.Unmarshal(payload); err != nil {
		return false
	}

	return vp8.IsKeyFrame
}

// IsVP9KeyFrame detects if vp9 payload is a keyframe
// taken from https://github.com/jech/galene/blob/master/codecs/codecs.go
// all credits belongs to Juliusz Chroboczek @jech and the awesome Galene SFU
func IsVP9KeyFrame(vp9 *codecs.VP9Packet, payload []byte) bool {
	if vp9 == nil {
		vp9 = &codecs.VP9Packet{}
		_, err := vp9.Unmarshal(payload)
		if err != nil || len(vp9.Payload) < 1 {
			return false
		}
	}

	if !vp9.B {
		return false
	}

	if (vp9.Payload[0] & 0xc0) != 0x80 {
		return false
	}

	profile := (vp9.Payload[0] >> 4) & 0x3
	if profile != 3 {
		return (vp9.Payload[0] & 0xC) == 0
	}
	return (vp9.Payload[0] & 0x6) == 0
}

// IsAV1KeyFrame detects if av1 payload is a keyframe
// taken from https://github.com/jech/galene/blob/master/codecs/codecs.go
// all credits belongs to Juliusz Chroboczek @jech and the awesome Galene SFU
func IsAV1KeyFrame(payload []byte) bool {
	if len(payload) < 2 {
		return false
	}
	// Z=0, N=1
	if (payload[0] & 0x88) != 0x08 {
		return false
	}
	w := (payload[0] & 0x30) >> 4

	getObu := func(data []byte, last bool) ([]byte, int, bool) {
		if last {
			return data, len(data), false
		}
		offset := 0
		length := 0
		for {
			if len(data) <= offset {
				return nil, offset, offset > 0
			}
			if offset >= 4 {
				return nil, offset, true
			}
			l := data[offset]
			length |= int(l&0x7f) << (offset * 7)
			offset++
			if (l & 0x80) == 0 {
				break
			}
		}
		if len(data) < offset+length {
			return data[offset:], len(data), true
		}
		return data[offset : offset+length], offset + length, false
	}
	offset := 1
	i := 0
	for {
		obu, length, truncated :=
			getObu(payload[offset:], int(w) == i+1)
		if len(obu) < 1 {
			return false
		}
		tpe := (obu[0] & 0x38) >> 3
		switch i {
		case 0:
			// OBU_SEQUENCE_HEADER
			if tpe != 1 {
				return false
			}
		default:
			// OBU_FRAME_HEADER or OBU_FRAME
			if tpe == 3 || tpe == 6 {
				if len(obu) < 2 {
					return false
				}
				// show_existing_frame == 0
				if (obu[1] & 0x80) != 0 {
					return false
				}
				// frame_type == KEY_FRAME
				return (obu[1] & 0x60) == 0
			}
		}
		if truncated || i >= int(w) {
			// the first frame header is in a second
			// packet, give up.
			return false
		}
		offset += length
		i++
	}
}
