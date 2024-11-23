package pacer

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gammazero/deque"
	"github.com/livekit/protocol/logger"
)

type queueEntry struct {
	timestamp time.Time
	size      int
}

type PacerIntervalQueue struct {
	*Base
	logger logger.Logger
	lock   sync.RWMutex

	targetLatency time.Duration

	packets    deque.Deque[*Packet]
	queueBytes int

	queueBitrate        int
	queueBitrateEntries []*queueEntry
	lastWaitTime        time.Duration

	isStopped atomic.Bool

	paused   bool
	unpaused chan struct{}
}

func NewPacerIntervalQueue(targetLatency time.Duration, logger logger.Logger) *PacerIntervalQueue {
	p := &PacerIntervalQueue{
		Base:          NewBase(logger),
		targetLatency: targetLatency,
		logger:        logger,
	}
	p.pause()
	p.packets.SetMinCapacity(9)
	return p
}

func (p *PacerIntervalQueue) Start() {
	if !p.isStopped.Load() {
		go p.sendWorker()
		go p.bitrateWorker()
	}
}

func (p *PacerIntervalQueue) Enqueue(pkt *Packet) {
	if p.isStopped.Load() {
		return
	}

	pktSize := pkt.getPktSize()

	p.lock.Lock()
	p.packets.PushBack(pkt)
	p.queueBitrateEntries = append(p.queueBitrateEntries, &queueEntry{
		timestamp: time.Now(),
		size:      pktSize,
	})
	p.queueBytes += pktSize

	p.unpause()
	p.lock.Unlock()
}

func (p *PacerIntervalQueue) Stop() {
	p.isStopped.Store(true)
}

func (p *PacerIntervalQueue) SetBitrate(bitrate int) {
	p.lock.Lock()

	p.lock.Unlock()
}

func (p *PacerIntervalQueue) bitrateWorker() {
	for !p.isStopped.Load() {
		p.lock.Lock()
		p.refreshQueueBitrate()
		p.lock.Unlock()
		time.Sleep(1 * time.Second)
	}
}

func (p *PacerIntervalQueue) refreshQueueBitrate() {
	totalBytes := 0
	earliestTime := time.Now()

	for i := 0; i < len(p.queueBitrateEntries); i++ {
		entry := p.queueBitrateEntries[i]
		totalBytes += entry.size
		if entry.timestamp.Before(earliestTime) {
			earliestTime = entry.timestamp
		}
	}

	p.queueBitrateEntries = []*queueEntry{}

	elapsed := time.Since(earliestTime)

	if elapsed.Milliseconds() < 500 {
		return
	}

	nextBitrate := int(float64(totalBytes) * 8 / elapsed.Seconds())
	currentBitrate := p.queueBitrate

	// if next bitrate is more than 10% less than current, use current minus 10% to cap
	if float64(nextBitrate) < float64(currentBitrate)*0.9 {
		nextBitrate = int(float64(currentBitrate) * 0.9)
	}

	slog.Info("Queue bitrate updated", "next", nextBitrate, "previous", currentBitrate, "elapsed", elapsed.Seconds(), "queueBytes", p.queueBytes, "waitTime", p.lastWaitTime.Milliseconds())

	p.queueBitrate = nextBitrate
}

func (p *PacerIntervalQueue) calculationIntervalForPacketSize(pktSize int) time.Duration {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.queueBitrate == 0 || p.targetLatency.Seconds() == 0 || pktSize == 0 {
		return 0
	}

	duration := time.Duration(((float64(pktSize) * 8) / float64(p.queueBitrate)) * float64(time.Second))

	latencyThreshold := float64(p.targetLatency.Seconds() * float64(p.queueBitrate) / 8)

	// if queue is too full, decrease interval by increasing backpressure
	if float64(p.queueBytes)-latencyThreshold > 0 {
		duration = duration * time.Duration(latencyThreshold/float64(p.queueBytes))
	}

	p.lastWaitTime = duration

	if duration > 1*time.Millisecond {
		return 1 * time.Millisecond
	}

	return duration
}

func (p *PacerIntervalQueue) pause() {
	if p.paused {
		return
	}

	p.unpaused = make(chan struct{})
	p.paused = true
}

func (p *PacerIntervalQueue) unpause() {
	if !p.paused {
		return
	}

	p.paused = false
	close(p.unpaused)
}

func (p *PacerIntervalQueue) sendWorker() {
	for !p.isStopped.Load() {
		if p.paused {
			<-p.unpaused
		}

		p.lock.Lock()
		if p.packets.Len() == 0 {
			p.lock.Unlock()
			// allow overshoot in next interval with shortage in this interval
			p.pause()

			continue
		}

		pkt := p.packets.PopFront()
		pktSize := pkt.getPktSize()
		p.queueBytes -= pktSize
		p.lock.Unlock()

		p.Base.SendPacket(pkt)

		interval := p.calculationIntervalForPacketSize(pktSize)

		time.Sleep(interval)
	}
}

// ------------------------------------------------
