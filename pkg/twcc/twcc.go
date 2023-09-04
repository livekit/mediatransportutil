package twcc

import (
	"math/rand"
	"sync"

	piontwcc "github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/rtcp"
)

const (
	tccReportDelta          = 1e8
	tccReportDeltaAfterMark = 50e6
)

// Responder is a lightweight wrapper around pion/interceptor's implementation of TWCC
type Responder struct {
	sync.Mutex

	mSSRC      uint32
	sSSRC      uint32
	lastReport int64
	recorder   *piontwcc.Recorder

	onFeedback func(packet []rtcp.Packet)
}

func NewTransportWideCCResponder(mSSRC uint32) *Responder {
	sSSRC := rand.Uint32()
	recorder := piontwcc.NewRecorder(sSSRC)
	return &Responder{
		sSSRC:    sSSRC,
		mSSRC:    mSSRC,
		recorder: recorder,
	}
}

// Push a sequence number read from rtp packet ext packet
func (t *Responder) Push(sn uint16, timeNS int64, marker bool) {
	t.Lock()
	defer t.Unlock()

	t.recorder.Record(t.mSSRC, sn, timeNS/1000)

	delta := timeNS - t.lastReport
	if t.recorder.PacketsHeld() > 20 && t.mSSRC != 0 &&
		(delta >= tccReportDelta ||
			t.recorder.PacketsHeld() > 100 ||
			(marker && delta >= tccReportDeltaAfterMark)) {
		if pkts := t.recorder.BuildFeedbackPacket(); pkts != nil {
			t.onFeedback(pkts)
		}
		t.lastReport = timeNS
	}
}

// OnFeedback sets the callback for the formed twcc feedback rtcp packet
func (t *Responder) OnFeedback(f func(pkts []rtcp.Packet)) {
	t.onFeedback = f
}
