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

package mediatransportutil

import (
	"fmt"
	"testing"
	"time"

	"github.com/livekit/protocol/utils"
	"github.com/stretchr/testify/require"
)

func Test_timeToNtp(t *testing.T) {
	type args struct {
		ns time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantNTP uint64
	}{
		{
			name: "Must return correct NTP time",
			args: args{
				ns: time.Unix(1602391458, 1234),
			},
			wantNTP: 16369753560730047668,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			gotNTP := uint64(ToNtpTime(tt.args.ns))
			if gotNTP != tt.wantNTP {
				t.Errorf("timeToNtp() gotFraction = %v, want %v", gotNTP, tt.wantNTP)
			}
		})
	}
}

func Test_TimeSync(t *testing.T) {
	cl := &utils.SimulatedClock{}

	s := &TimeSyncInfo{
		clock: cl,
	}

	cl.Set(time.Unix(1, 0))

	req := s.StartTimeSync()

	cl.Set(time.Unix(1_000_000, 0))

	resp := handleTimeSyncRequestWithClock(cl, req)

	cl.Set(time.Unix(11, 0))

	err := s.HandleTimeSyncResponse(resp)
	require.NoError(t, err)

	fmt.Println(s)

	pt := s.GetPeerTimeForLocalTime(time.Unix(10, 0))
	require.Equal(t, time.Unix(1_000_005, 0).UnixNano(), pt.UnixNano())
}
