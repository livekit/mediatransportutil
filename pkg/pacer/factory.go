package pacer

import (
	"fmt"
	"time"

	"github.com/livekit/protocol/logger"
)

type PacerType int

const (
	PassThroughPacer PacerType = iota
	NoQueuePacer
	LeakyBucketPacer
)

type pacerFactorParams struct {
	SendInterval time.Duration
	Bitrate      int
	MaxLatency   time.Duration
	PacerType    PacerType
	Logger       logger.Logger
}

var defaultPacerParams = pacerFactorParams{
	SendInterval: 5 * time.Millisecond,
	Bitrate:      5000000,
	MaxLatency:   2 * time.Second,
}

type PacketFactoryOpt func(params *pacerFactorParams)

func WithBitrate(bitrate int) PacketFactoryOpt {
	return func(params *pacerFactorParams) {
		params.Bitrate = bitrate
	}
}

func WithMaxLatency(maxLatency time.Duration) PacketFactoryOpt {
	return func(params *pacerFactorParams) {
		params.MaxLatency = maxLatency
	}
}

func Withlogger(logger logger.Logger) PacketFactoryOpt {
	return func(params *pacerFactorParams) {
		params.Logger = logger
	}
}

type PacerFactory struct {
	params *pacerFactorParams
}

func NewPacerFactory(pacerType PacerType, opts ...PacketFactoryOpt) Factory {
	params := defaultPacerParams
	params.PacerType = pacerType
	params.Logger = logger.GetLogger()
	for _, o := range opts {
		o(&params)
	}
	return &PacerFactory{
		params: &params,
	}
}

func (f *PacerFactory) NewPacer() (Pacer, error) {
	switch f.params.PacerType {
	case PassThroughPacer:
		return NewPassThrough(f.params.Logger), nil
	case NoQueuePacer:
		return NewNoQueue(f.params.Logger), nil
	case LeakyBucketPacer:
		return NewPacerLeakyBucket(f.params.SendInterval, f.params.Bitrate, f.params.MaxLatency, f.params.Logger), nil
	default:
		return nil, fmt.Errorf("unknown pacer type: %v", f.params.PacerType)
	}
}
