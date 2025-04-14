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

package g711

import (
	"fmt"
	"io"

	prtp "github.com/pion/rtp"

	"github.com/livekit/mediatransportutil/pkg/audio"
	"github.com/livekit/mediatransportutil/pkg/audio/rtp"
)

const ULawSDPName = "PCMU/8000"

func init() {
	audio.RegisterCodec(rtp.NewAudioCodec(audio.CodecInfo{
		SDPName:     ULawSDPName,
		SampleRate:  8000,
		RTPDefType:  prtp.PayloadTypePCMU,
		RTPIsStatic: true,
		Priority:    -10,
		FileExt:     "g711u",
	}, DecodeULaw, EncodeULaw))
}

type ULawSample []byte

func (s ULawSample) Size() int {
	return len(s)
}

func (s ULawSample) CopyTo(dst []byte) (int, error) {
	if len(dst) < len(s) {
		return 0, io.ErrShortBuffer
	}
	n := copy(dst, s)
	return n, nil
}

func (s ULawSample) Decode() audio.PCM16Sample {
	out := make(audio.PCM16Sample, len(s))
	DecodeULawTo(out, s)
	return out
}

func (s *ULawSample) Encode(data audio.PCM16Sample) {
	out := make(ULawSample, len(data))
	EncodeULawTo(out, data)
	*s = out
}

type ULawWriter = audio.WriteCloser[ULawSample]

type ULawDecoder struct {
	w   audio.PCM16Writer
	buf audio.PCM16Sample
}

func (d *ULawDecoder) String() string {
	return fmt.Sprintf("PCMU(decode) -> %s", d.w)
}

func (d *ULawDecoder) SampleRate() int {
	return d.w.SampleRate()
}

func (d *ULawDecoder) Close() error {
	return d.w.Close()
}

func (d *ULawDecoder) WriteSample(in ULawSample) error {
	if len(in) >= cap(d.buf) {
		d.buf = make(audio.PCM16Sample, len(in))
	} else {
		d.buf = d.buf[:len(in)]
	}
	DecodeULawTo(d.buf, in)
	return d.w.WriteSample(d.buf)
}

func DecodeULaw(w audio.PCM16Writer) ULawWriter {
	switch w.SampleRate() {
	default:
		w = audio.ResampleWriter(w, 8000)
	case 8000:
	}
	return &ULawDecoder{w: w}
}

type ULawEncoder struct {
	w   ULawWriter
	buf ULawSample
}

func (e *ULawEncoder) String() string {
	return fmt.Sprintf("PCMU(encode) -> %s", e.w)
}

func (e *ULawEncoder) SampleRate() int {
	return e.w.SampleRate()
}

func (e *ULawEncoder) Close() error {
	return e.w.Close()
}

func (e *ULawEncoder) WriteSample(in audio.PCM16Sample) error {
	if len(in) >= cap(e.buf) {
		e.buf = make(ULawSample, len(in))
	} else {
		e.buf = e.buf[:len(in)]
	}
	EncodeULawTo(e.buf, in)
	return e.w.WriteSample(e.buf)
}

func EncodeULaw(w ULawWriter) audio.PCM16Writer {
	switch w.SampleRate() {
	default:
		panic("unsupported sample rate")
	case 8000:
	}
	return &ULawEncoder{w: w}
}
