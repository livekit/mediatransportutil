package twcc

import (
	"testing"

	"github.com/pion/rtcp"
	"github.com/stretchr/testify/assert"
)

const (
	invalidmSSRC = 0
	validmSSRC   = 1
)

type testPacket struct {
	sn     uint16
	timeNS int64
	marker bool
}

func makeTestPackets(startTm int64, startSN uint16, length uint16) []testPacket {
	pkts := make([]testPacket, 0)
	for i := uint16(0); i < length; i++ {
		pkts = append(pkts, testPacket{startSN + i, startTm + int64(i), false})
	}
	return pkts
}

func TestBuildFeedbackOnPush(t *testing.T) {
	testCases := []struct {
		name                    string
		mSSRC                   uint32
		pkts                    []testPacket
		expectedFeedbackPackets int
	}{
		{
			name:                    "should not build when fewer than 21 packets",
			mSSRC:                   validmSSRC,
			expectedFeedbackPackets: 0,
			pkts:                    makeTestPackets(tccReportDelta, 1, 20),
		},
		{
			name:                    "should not build when mSSRC is invalid",
			mSSRC:                   invalidmSSRC,
			expectedFeedbackPackets: 0,
			pkts:                    makeTestPackets(tccReportDelta, 1, 21),
		},
		{
			name:                    "should not build when delta is too small",
			mSSRC:                   validmSSRC,
			expectedFeedbackPackets: 0,
			pkts:                    makeTestPackets(0, 1, 21),
		},
		{
			name:                    "should build when more than 20 packets and delta is sufficient",
			mSSRC:                   validmSSRC,
			expectedFeedbackPackets: 1,
			pkts:                    makeTestPackets(tccReportDelta, 1, 21),
		},
		{
			name:                    "should build when more than 100 packets regardless of delta",
			mSSRC:                   validmSSRC,
			expectedFeedbackPackets: 1,
			pkts:                    makeTestPackets(0, 1, 101),
		},
		{
			name:                    "should build when more than 20 packets and marker with sufficient delta",
			mSSRC:                   validmSSRC,
			expectedFeedbackPackets: 1,
			pkts: func() []testPacket {
				pkts := makeTestPackets(tccReportDeltaAfterMark, 1, 21)
				pkts[len(pkts)-1].marker = true // update final packet to be marker
				return pkts
			}(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var fbrecv int
			responder := NewTransportWideCCResponder(tc.mSSRC)
			responder.OnFeedback(func(pkts []rtcp.Packet) { fbrecv += len(pkts) })
			for _, pkt := range tc.pkts {
				responder.Push(pkt.sn, pkt.timeNS, pkt.marker)
			}
			assert.Equal(t, tc.expectedFeedbackPackets, fbrecv)
		})
	}
}
