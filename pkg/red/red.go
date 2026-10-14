// Copyright 2026 LiveKit, Inc.
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

package red

import (
	"encoding/binary"
	"errors"

	"github.com/pion/rtp"
)

const maxRecoverCount = 2

var (
	ErrIncompleteHeader = errors.New("incomplete red block header")
	ErrIncompleteBlock  = errors.New("incomplete red block payload")
)

// Depacketizer extracts primary packets from a RED stream, recovering lost ones from redundant blocks.
// The zero value is ready to use; it is not safe for concurrent use.
type Depacketizer struct {
	started bool
	lastSeq uint16

	// bit k set means lastSeq-1-k was received
	pktHistory byte
}

// Depacketize returns the packets to emit for a RED packet: recovered ones oldest first, then the primary.
func (d *Depacketizer) Depacketize(pkt *rtp.Packet) ([]*rtp.Packet, error) {
	var needRecover bool
	if !d.started {
		d.lastSeq = pkt.SequenceNumber
		d.pktHistory = 0
		d.started = true
	} else {
		diff := pkt.SequenceNumber - d.lastSeq
		switch {
		case diff == 0: // duplicate
			break

		case diff > 0x8000: // unorder
			// in history
			if 65535-diff < 8 {
				d.pktHistory |= 1 << (65535 - diff)
				needRecover = true
			}

		case diff > 8: // long jump
			d.lastSeq = pkt.SequenceNumber
			d.pktHistory = 0
			needRecover = true

		default:
			d.lastSeq = pkt.SequenceNumber
			d.pktHistory = (d.pktHistory << byte(diff)) | 1<<(diff-1)
			needRecover = true
		}
	}

	var recoverBits byte
	if needRecover {
		bitIndex := d.lastSeq - pkt.SequenceNumber
		for i := range maxRecoverCount {
			if bitIndex > 7 {
				break
			}
			if d.pktHistory&byte(1<<bitIndex) == 0 {
				recoverBits |= byte(1 << i)
			}
			bitIndex++
		}
	}

	return ExtractPackets(pkt, recoverBits)
}

type block struct {
	tsOffset uint32
	length   int
	pt       uint8
	primary  bool
}

// ExtractPackets splits a RED packet into the redundant blocks flagged in recoverBits, oldest first, then the primary.
// Bit i of recoverBits selects the block i+1 packets before the primary; payloads alias pkt.Payload.
func ExtractPackets(pkt *rtp.Packet, recoverBits byte) ([]*rtp.Packet, error) {
	payload := pkt.Payload
	var blocks []block
	var blockLength int
	for {
		if len(payload) < 1 {
			// need at least one byte for primary encoding
			return nil, ErrIncompleteHeader
		}

		if payload[0]&0x80 == 0 {
			// last block is primary encoding data
			pt := uint8(payload[0] & 0x7F)

			blocks = append(blocks, block{pt: pt, primary: true})

			payload = payload[1:]
			break
		} else {
			if len(payload) < 4 {
				return nil, ErrIncompleteHeader
			}

			// RFC 2198 §3 block header: F(1) | PT(7) | ts offset(14) | length(10)
			blockHead := binary.BigEndian.Uint32(payload[0:])
			length := int(blockHead & 0x03FF)
			blockHead >>= 10
			tsOffset := blockHead & 0x3FFF
			blockHead >>= 14
			pt := uint8(blockHead & 0x7F)

			blocks = append(blocks, block{pt: pt, length: length, tsOffset: tsOffset})

			blockLength += length
			payload = payload[4:]
		}
	}

	if len(payload) < blockLength {
		return nil, ErrIncompleteBlock
	}

	pkts := make([]*rtp.Packet, 0, len(blocks))
	for i, b := range blocks {
		if b.primary {
			header := pkt.Header
			header.PayloadType = b.pt
			pkts = append(pkts, &rtp.Packet{Header: header, Payload: payload})
			break
		}

		recoverIndex := len(blocks) - i - 1
		if recoverIndex < 1 || recoverBits&(1<<(recoverIndex-1)) == 0 {
			// skip past packet/block that does not need recovery
			payload = payload[b.length:]
			continue
		}

		header := pkt.Header
		header.SequenceNumber -= uint16(recoverIndex)
		header.Timestamp -= b.tsOffset
		header.PayloadType = b.pt
		pkts = append(pkts, &rtp.Packet{Header: header, Payload: payload[:b.length]})

		payload = payload[b.length:]
	}

	return pkts, nil
}

// ExtractPrimaryEncoding returns the primary block of a RED payload, aliasing payload.
func ExtractPrimaryEncoding(payload []byte) ([]byte, error) {
	var blockLength int
	for {
		if len(payload) < 1 {
			// need at least one byte for primary encoding
			return nil, ErrIncompleteHeader
		}

		if payload[0]&0x80 == 0 {
			// last block is primary encoding data
			payload = payload[1:]
			break
		} else {
			if len(payload) < 4 {
				return nil, ErrIncompleteHeader
			}

			blockLength += int(binary.BigEndian.Uint16(payload[2:]) & 0x03FF)
			payload = payload[4:]
		}
	}

	if len(payload) < blockLength {
		return nil, ErrIncompleteBlock
	}

	return payload[blockLength:], nil
}
