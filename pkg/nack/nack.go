package nack

import (
	"math"
	"time"

	"github.com/pion/rtcp"
)

const (
	defaultRtt    = 70                     // default RTT in ms
	maxNacks      = 100                    // Max NACK sn the sfu will keep reference
	maxTries      = 5                      // Max number of times a packet will be NACKed
	minInterval   = 20 * time.Millisecond  // minimum interval between NACK tries for the same sequence number
	maxInterval   = 400 * time.Millisecond // maximum interval between NACK tries for the same sequence number
	backoffFactor = float64(1.25)
)

type NackQueueParams struct {
	DefaultRtt    uint32
	MaxNacks      int
	MaxTries      uint8
	MinInterval   time.Duration
	MaxInterval   time.Duration
	BackoffFactor float64
}

var NackQueueParamsDefault = NackQueueParams{
	DefaultRtt:    defaultRtt,
	MaxNacks:      maxNacks,
	MaxTries:      maxTries,
	MinInterval:   minInterval,
	MaxInterval:   maxInterval,
	BackoffFactor: backoffFactor,
}

type NackQueue struct {
	params     NackQueueParams
	nackParams nackParams

	nacks []*nack
	rtt   uint32
}

func NewNACKQueue(params NackQueueParams) *NackQueue {
	return &NackQueue{
		params: params,
		nackParams: nackParams{
			maxTries:      params.MaxTries,
			minInterval:   params.MinInterval,
			maxInterval:   params.MaxInterval,
			backoffFactor: params.BackoffFactor,
		},
		nacks: make([]*nack, 0, params.MaxNacks),
		rtt:   params.DefaultRtt,
	}
}

func (n *NackQueue) SetRTT(rtt uint32) {
	if rtt == 0 {
		n.rtt = n.params.DefaultRtt
	} else {
		n.rtt = rtt
	}
}

func (n *NackQueue) Remove(sn uint16) {
	for idx, nack := range n.nacks {
		if nack.seqNum != sn {
			continue
		}

		copy(n.nacks[idx:], n.nacks[idx+1:])
		n.nacks = n.nacks[:len(n.nacks)-1]
		break
	}
}

func (n *NackQueue) Push(sn uint16) {
	// if at capacity, pop the first one
	if len(n.nacks) == cap(n.nacks) {
		copy(n.nacks[0:], n.nacks[1:])
		n.nacks = n.nacks[:len(n.nacks)-1]
	}

	n.nacks = append(n.nacks, newNack(&n.nackParams, sn))
}

func (n *NackQueue) Pairs() ([]rtcp.NackPair, int) {
	if len(n.nacks) == 0 {
		return nil, 0
	}

	now := time.Now()

	// set it far back to get the first pair
	baseSN := n.nacks[0].seqNum - 17

	snsToPurge := make([]uint16, 0)

	numSeqNumsNacked := 0
	isPairActive := false
	var np rtcp.NackPair
	var nps []rtcp.NackPair
	for _, nack := range n.nacks {
		shouldSend, shouldRemove, sn := nack.getNack(now, n.rtt)
		if shouldRemove {
			snsToPurge = append(snsToPurge, sn)
			continue
		}
		if !shouldSend {
			continue
		}

		numSeqNumsNacked++
		if (sn - baseSN) > 16 {
			// need a new nack pair
			if isPairActive {
				nps = append(nps, np)
				isPairActive = false
			}

			baseSN = sn

			np.PacketID = sn
			np.LostPackets = 0

			isPairActive = true
		} else {
			np.LostPackets |= 1 << (sn - baseSN - 1)
		}
	}

	// add any left over
	if isPairActive {
		nps = append(nps, np)
	}

	for _, sn := range snsToPurge {
		n.Remove(sn)
	}

	return nps, numSeqNumsNacked
}

// -----------------------------------------------------------------

type nackParams struct {
	maxTries      uint8
	minInterval   time.Duration
	maxInterval   time.Duration
	backoffFactor float64
}

type nack struct {
	params *nackParams

	seqNum       uint16
	tries        uint8
	lastNackedAt time.Time
}

func newNack(params *nackParams, sn uint16) *nack {
	return &nack{
		params:       params,
		seqNum:       sn,
		tries:        0,
		lastNackedAt: time.Now(),
	}
}

func (n *nack) getNack(now time.Time, rtt uint32) (shouldSend bool, shouldRemove bool, sn uint16) {
	sn = n.seqNum
	if n.tries >= n.params.maxTries {
		shouldRemove = true
		return
	}

	var requiredInterval time.Duration
	if n.tries > 0 {
		// exponentially backoff retries, but cap maximum spacing between retries
		requiredInterval = n.params.maxInterval
		backoffInterval := time.Duration(float64(rtt)*math.Pow(n.params.backoffFactor, float64(n.tries-1))) * time.Millisecond
		if backoffInterval < requiredInterval {
			requiredInterval = backoffInterval
		}
	}
	if requiredInterval < n.params.minInterval {
		//
		// Wait for some time for out-of-order packets before NACKing even if before NACKing first time.
		// For subsequent tries, maintain minimum spacing.
		//
		requiredInterval = n.params.minInterval
	}

	if now.Sub(n.lastNackedAt) < requiredInterval {
		return
	}

	n.tries++
	n.lastNackedAt = now
	shouldSend = true
	return
}
