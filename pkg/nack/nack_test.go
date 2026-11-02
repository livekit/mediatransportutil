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

package nack

import (
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/stretchr/testify/require"
)

func Test_nackQueue_pairs(t *testing.T) {
	type PairsResult struct {
		pairs            []rtcp.NackPair
		numSeqNumsNacked int
	}

	tests := []struct {
		name string
		args []uint16
		want PairsResult
	}{
		{
			name: "Must return correct single pairs pair",
			args: []uint16{1, 2, 4, 5},
			want: PairsResult{
				pairs: []rtcp.NackPair{
					{
						PacketID:    1,
						LostPackets: 13,
					},
				},
				numSeqNumsNacked: 4,
			},
		},
		{
			name: "Must return correct pair wrap",
			args: []uint16{65533, 2, 4, 5, 30, 32},
			want: PairsResult{
				pairs: []rtcp.NackPair{
					{
						PacketID:    65533,
						LostPackets: 1<<7 + 1<<6 + 1<<4,
					},
					{
						PacketID:    30,
						LostPackets: 1 << 1,
					},
				},
				numSeqNumsNacked: 6,
			},
		},
		{
			name: "Must return 2 pairs pair",
			args: []uint16{1, 2, 4, 5, 20, 22, 24, 27},
			want: PairsResult{
				pairs: []rtcp.NackPair{
					{
						PacketID:    1,
						LostPackets: 13,
					},
					{
						PacketID:    20,
						LostPackets: 74,
					},
				},
				numSeqNumsNacked: 8,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, n := range []NackQueueInterface{NewNACKQueue(NackQueueParamsDefault), NewNACKQueueSafe(NackQueueParamsDefault)} {
				for _, sn := range tt.args {
					n.Push(sn)
				}
				time.Sleep(100 * time.Millisecond)
				got, numSeqNumsNacked := n.Pairs()
				require.EqualValues(t, tt.want.pairs, got)
				require.Equal(t, tt.want.numSeqNumsNacked, numSeqNumsNacked)
			}
		})
	}
}

func Test_nackQueue_push(t *testing.T) {
	type args struct {
		sn []uint16
	}
	tests := []struct {
		name string
		args args
		want []uint16
	}{
		{
			name: "Must keep packet order",
			args: args{
				sn: []uint16{1, 3, 4, 5, 7, 8},
			},
			want: []uint16{1, 3, 4, 5, 7, 8},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, n := range []NackQueueInterface{NewNACKQueue(NackQueueParamsDefault), NewNACKQueueSafe(NackQueueParamsDefault)} {
				for _, sn := range tt.args.sn {
					n.Push(sn)
				}
				var newSN []uint16
				for _, nack := range n.Nacks() {
					newSN = append(newSN, nack.seqNum)
				}
				require.Equal(t, tt.want, newSN)
			}
		})
	}
}

func Test_nackQueue_remove(t *testing.T) {
	type args struct {
		sn []uint16
	}
	tests := []struct {
		name string
		args args
		want []uint16
	}{
		{
			name: "Must keep packet order",
			args: args{
				sn: []uint16{1, 3, 4, 5, 7, 8},
			},
			want: []uint16{1, 3, 4, 7, 8},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, n := range []NackQueueInterface{NewNACKQueue(NackQueueParamsDefault), NewNACKQueueSafe(NackQueueParamsDefault)} {
				for _, sn := range tt.args.sn {
					n.Push(sn)
				}
				n.Remove(5)
				var newSN []uint16
				for _, nack := range n.Nacks() {
					newSN = append(newSN, nack.seqNum)
				}
				require.Equal(t, tt.want, newSN)
			}
		})
	}
}

func Test_nackQueue_pushRange(t *testing.T) {
	small := NackQueueParamsDefault
	small.MaxNacks = 3

	tests := []struct {
		name   string
		params NackQueueParams
		end    uint16
		count  uint64
		want   []uint16
	}{
		{name: "empty range", params: NackQueueParamsDefault, end: 10, count: 0},
		{name: "short range", params: NackQueueParamsDefault, end: 10, count: 3, want: []uint16{7, 8, 9}},
		{name: "range wraps", params: NackQueueParamsDefault, end: 2, count: 4, want: []uint16{65534, 65535, 0, 1}},
		{name: "gap larger than queue", params: small, end: 100, count: 1 << 40, want: []uint16{97, 98, 99}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, n := range []NackQueueInterface{NewNACKQueue(tt.params), NewNACKQueueSafe(tt.params)} {
				n.PushRange(tt.end, tt.count)
				var got []uint16
				for _, nack := range n.Nacks() {
					got = append(got, nack.seqNum)
				}
				require.Equal(t, tt.want, got)
			}
		})
	}
}
