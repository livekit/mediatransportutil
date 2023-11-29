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

package pacer

import (
	"errors"
	"io"
	"time"

	"github.com/livekit/protocol/logger"
	"github.com/pion/rtp"
)

type Base struct {
	logger logger.Logger

	packetTime *PacketTime
}

func NewBase(logger logger.Logger) *Base {
	return &Base{
		logger:     logger,
		packetTime: NewPacketTime(),
	}
}

func (b *Base) SetInterval(_interval time.Duration) {
}

func (b *Base) SetBitrate(_bitrate int) {
}

func (b *Base) SendPacket(p *Packet) (int, error) {
	defer func() {
		if p.Pool != nil && p.PoolEntity != nil {
			p.Pool.Put(p.PoolEntity)
		}
	}()

	_, err := b.writeRTPHeaderExtensions(p)
	if err != nil {
		b.logger.Errorw("writing rtp header extensions err", err)
		return 0, err
	}

	var written int
	written, err = p.Writer(p.Header, p.Payload)
	if err != nil {
		if !errors.Is(err, io.ErrClosedPipe) {
			b.logger.Errorw("write rtp packet failed", err)
		}
		return 0, err
	}

	return written, nil
}

// writes RTP header extensions of track
func (b *Base) writeRTPHeaderExtensions(p *Packet) (time.Time, error) {
	for _, ext := range p.Extensions {
		if ext.ID == 0 || len(ext.Payload) == 0 {
			continue
		}

		p.Header.SetExtension(ext.ID, ext.Payload)
	}

	sendingAt := b.packetTime.Get()
	if p.AbsSendTimeExtID != 0 {
		sendTime := rtp.NewAbsSendTimeExtension(sendingAt)
		b, err := sendTime.Marshal()
		if err != nil {
			return time.Time{}, err
		}

		err = p.Header.SetExtension(p.AbsSendTimeExtID, b)
		if err != nil {
			return time.Time{}, err
		}
	}

	return sendingAt, nil
}

// ------------------------------------------------
