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
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

const (
	tsStep = uint32(48000 / 1000 * 10)
	redPT  = 63
)

// one redundant block (PT 111, ts offset 960, length 1) then primary PT 111
var goldenPayload = []byte{0xEF, 0x0F, 0x00, 0x01, 0x6F, 0xCC, 0xAA, 0xBB}

func TestExtractPackets(t *testing.T) {
	t.Run("golden", func(t *testing.T) {
		pkt := &rtp.Packet{
			Header:  rtp.Header{SequenceNumber: 100, Timestamp: 10000, PayloadType: redPT},
			Payload: goldenPayload,
		}
		pkts, err := ExtractPackets(pkt, 0xFF)
		require.NoError(t, err)
		requirePktsEqual(t, []*rtp.Packet{
			{Header: rtp.Header{SequenceNumber: 99, Timestamp: 9040, PayloadType: 111}, Payload: []byte{0xCC}},
			{Header: rtp.Header{SequenceNumber: 100, Timestamp: 10000, PayloadType: 111}, Payload: []byte{0xAA, 0xBB}},
		}, pkts)
	})

	header := rtp.Header{SequenceNumber: 65534, Timestamp: (uint32(1) << 31) - 2*tsStep, PayloadType: 111}
	primaries := generatePkts(header, 3, tsStep)
	redPkt := generateRedPkts(primaries, 2)[2]

	for _, tc := range []struct {
		name        string
		recoverBits byte
		want        []*rtp.Packet
	}{
		{"all", 0xFF, primaries},
		{"none", 0, primaries[2:]},
		{"newest redundant", 0b01, primaries[1:]},
		{"oldest redundant", 0b10, []*rtp.Packet{primaries[0], primaries[2]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkts, err := ExtractPackets(redPkt, tc.recoverBits)
			require.NoError(t, err)
			requirePktsEqual(t, tc.want, pkts)
		})
	}
}

func TestExtractPrimaryEncoding(t *testing.T) {
	header := rtp.Header{SequenceNumber: 65530, Timestamp: (uint32(1) << 31) - 2*tsStep, PayloadType: 111}
	pkts := generatePkts(header, 10, tsStep)

	primaryPkts := make([]*rtp.Packet, 0, len(pkts))
	for _, redPkt := range generateRedPkts(pkts, 2) {
		payload, err := ExtractPrimaryEncoding(redPkt.Payload)
		require.NoError(t, err)
		primaryHeader := redPkt.Header
		primaryHeader.PayloadType = 111
		primaryPkts = append(primaryPkts, &rtp.Packet{Header: primaryHeader, Payload: payload})
	}
	requirePktsEqual(t, pkts, primaryPkts)

	payload, err := ExtractPrimaryEncoding(goldenPayload)
	require.NoError(t, err)
	require.Equal(t, []byte{0xAA, 0xBB}, payload)
}

func TestDepacketizer(t *testing.T) {
	var inOrder, duplicates, everyThird []int
	for i := range 19 {
		inOrder = append(inOrder, i)
		duplicates = append(duplicates, i)
		if i > 0 {
			duplicates = append(duplicates, i-1)
		}
		duplicates = append(duplicates, i)
		if i%3 == 0 {
			everyThird = append(everyThird, i)
		}
	}

	// indices into a stream starting at seq 65530, so index 6 is seq 0
	for _, tc := range []struct {
		name     string
		redCount int
		send     []int
		want     []int
	}{
		{"in order", 2, inOrder, inOrder},
		{"duplicate and out of order", 2, duplicates, duplicates},
		{"full recover", 2, everyThird, inOrder},
		{"lost 2, redundancy 1", 1, []int{0, 3, 6, 9, 12}, []int{0, 2, 3, 5, 6, 8, 9, 11, 12}},
		{"partial recover and long jump", 2, []int{0, 5, 12, 21, 24, 27}, []int{0, 3, 4, 5, 10, 11, 12, 19, 20, 21, 22, 23, 24, 25, 26, 27}},
		{"out of order", 2, []int{20, 10, 25, 23, 34}, []int{20, 10, 23, 24, 25, 21, 22, 23, 32, 33, 34}},
		{"gap of 1", 2, []int{0, 1, 3}, []int{0, 1, 2, 3}},
		{"gap of 3", 2, []int{0, 4}, []int{0, 2, 3, 4}},
		{"jump over 8", 2, []int{0, 1, 11}, []int{0, 1, 9, 10, 11}},
		// 7 behind recovers one block, 8 behind is in history but has no room, 9 behind is outside it
		{"history window edge", 2, []int{20, 13, 12, 11}, []int{20, 12, 13, 12, 11}},
		{"wraparound", 2, []int{0, 1, 2, 3, 4, 5, 7}, []int{0, 1, 2, 3, 4, 5, 6, 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := rtp.Header{SequenceNumber: 65530, Timestamp: (uint32(1) << 31) - 2*tsStep, PayloadType: 111}
			primaries := generatePkts(header, 50, tsStep)
			redPkts := generateRedPkts(primaries, tc.redCount)

			var d Depacketizer
			var got []*rtp.Packet
			for _, i := range tc.send {
				pkts, err := d.Depacketize(redPkts[i])
				require.NoError(t, err)
				got = append(got, pkts...)
			}

			want := make([]*rtp.Packet, 0, len(tc.want))
			for _, i := range tc.want {
				want = append(want, primaries[i])
			}
			requirePktsEqual(t, want, got)
		})
	}
}

func TestTruncated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		err     error
	}{
		{"empty", []byte{}, ErrIncompleteHeader},
		{"short redundant header", []byte{0x80, 0, 0}, ErrIncompleteHeader},
		{"no primary header", []byte{0xEF, 0x0F, 0x00, 0x01}, ErrIncompleteHeader},
		{"short block", []byte{0xEF, 0x0F, 0x00, 0x05, 0x6F, 0xCC}, ErrIncompleteBlock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractPrimaryEncoding(tc.payload)
			require.ErrorIs(t, err, tc.err)

			pkt := &rtp.Packet{Payload: tc.payload}
			_, err = ExtractPackets(pkt, 0xFF)
			require.ErrorIs(t, err, tc.err)

			_, err = new(Depacketizer).Depacketize(pkt)
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func generatePkts(header rtp.Header, count int, tsStep uint32) []*rtp.Packet {
	pkts := make([]*rtp.Packet, 0, count)
	for range count {
		hbuf, _ := header.Marshal()
		pkts = append(pkts, &rtp.Packet{
			Header:  header,
			Payload: hbuf,
		})
		header.SequenceNumber++
		header.Timestamp += tsStep
	}
	return pkts
}

func generateRedPkts(pkts []*rtp.Packet, redCount int) []*rtp.Packet {
	redPkts := make([]*rtp.Packet, 0, len(pkts))
	for i, pkt := range pkts {
		redPkt := *pkt
		redPkt.PayloadType = redPT
		redPkt.Payload = encodeRED(pkts[max(0, i-redCount):i], pkt)
		redPkts = append(redPkts, &redPkt)
	}
	return redPkts
}

func encodeRED(redundant []*rtp.Packet, primary *rtp.Packet) []byte {
	var out []byte
	for _, p := range redundant {
		out = binary.BigEndian.AppendUint32(out,
			(0x80|uint32(p.PayloadType))<<24|((primary.Timestamp-p.Timestamp)&0x3FFF)<<10|uint32(len(p.Payload))&0x3FF)
	}
	out = append(out, primary.PayloadType)
	for _, p := range redundant {
		out = append(out, p.Payload...)
	}
	return append(out, primary.Payload...)
}

func requirePktsEqual(t *testing.T, want, got []*rtp.Packet) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].SequenceNumber, got[i].SequenceNumber, "packet %d", i)
		require.Equal(t, want[i].Timestamp, got[i].Timestamp, "packet %d", i)
		require.Equal(t, want[i].PayloadType, got[i].PayloadType, "packet %d", i)
		require.Equal(t, want[i].Payload, got[i].Payload, "packet %d", i)
	}
}
