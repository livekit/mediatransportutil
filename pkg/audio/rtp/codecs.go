// Copyright 2024 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rtp

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/livekit/mediatransportutil/pkg/audio"
)

var (
	mediaID         atomic.Uint32
	mediaDumpToFile = os.Getenv("LK_DUMP_MEDIA") == "true"
)

var (
	codecByType [0xff]audio.Codec
)

func init() {
	audio.OnRegister(func(c audio.Codec) {
		info := c.Info()
		if info.RTPIsStatic {
			codecByType[info.RTPDefType] = c
		}
	})
}

func CodecByPayloadType(typ byte) audio.Codec {
	return codecByType[typ]
}

type AudioCodec interface {
	audio.Codec
	EncodeRTP(w *Stream) audio.PCM16Writer
	DecodeRTP(w audio.Writer[audio.PCM16Sample], typ byte) Handler
}

type AudioEncoder[S BytesFrame] interface {
	AudioCodec
	Decode(writer audio.PCM16Writer) audio.WriteCloser[S]
	Encode(writer audio.WriteCloser[S]) audio.PCM16Writer
}

func NewAudioCodec[S BytesFrame](
	info audio.CodecInfo,
	decode func(writer audio.PCM16Writer) audio.WriteCloser[S],
	encode func(writer audio.WriteCloser[S]) audio.PCM16Writer,
) AudioCodec {
	if info.SampleRate <= 0 {
		panic("invalid sample rate")
	}
	if info.RTPClockRate == 0 {
		info.RTPClockRate = info.SampleRate
	}
	return &audioCodec[S]{
		info:   info,
		encode: encode,
		decode: decode,
	}
}

type audioCodec[S BytesFrame] struct {
	info   audio.CodecInfo
	decode func(writer audio.PCM16Writer) audio.WriteCloser[S]
	encode func(writer audio.WriteCloser[S]) audio.PCM16Writer
}

func (c *audioCodec[S]) Info() audio.CodecInfo {
	return c.info
}

func (c *audioCodec[S]) Decode(w audio.PCM16Writer) audio.WriteCloser[S] {
	return c.decode(w)
}

func (c *audioCodec[S]) Encode(w audio.WriteCloser[S]) audio.PCM16Writer {
	return c.encode(w)
}

func (c *audioCodec[S]) EncodeRTP(w *Stream) audio.PCM16Writer {
	var s audio.WriteCloser[S] = NewMediaStreamOut[S](w, c.info.SampleRate)
	if mediaDumpToFile {
		id := mediaID.Add(1)
		name := fmt.Sprintf("sip_rtp_out_%d", id)
		ext := c.info.FileExt
		if ext == "" {
			ext = "raw"
		}
		s = audio.DumpWriter[S](ext, name, audio.NopCloser(s))
	}
	return c.encode(s)
}

func (c *audioCodec[S]) DecodeRTP(w audio.Writer[audio.PCM16Sample], typ byte) Handler {
	s := c.decode(audio.NopCloser(w))
	if mediaDumpToFile {
		id := mediaID.Add(1)
		name := fmt.Sprintf("sip_rtp_in_%d", id)
		ext := c.info.FileExt
		if ext == "" {
			ext = "raw"
		}
		s = audio.DumpWriter[S](ext, name, audio.NopCloser(s))
	}
	return NewMediaStreamIn(s)
}
