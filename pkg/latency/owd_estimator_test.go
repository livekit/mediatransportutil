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

package latency

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOWDEstimatorStepRiseWithoutLongTermDelta(t *testing.T) {
	params := OWDEstimatorParamsDefault
	params.PropagationDelayDeltaHighResetWait = 20 * time.Millisecond
	o := NewOWDEstimator(params)

	const interval = int64(3 * time.Second)
	sender := time.Now().UnixNano()
	report := func(delay time.Duration) (int64, bool) {
		sender += interval
		return o.Update(sender, sender+delay.Nanoseconds())
	}

	estimate, _ := report(100 * time.Millisecond)
	require.Equal(t, (100 * time.Millisecond).Nanoseconds(), estimate)

	// path change before any small delta was seen, long term delta is still zero
	stepped := false
	for i := 0; i < 5; i++ {
		var stepChange bool
		estimate, stepChange = report(300 * time.Millisecond)
		stepped = stepped || stepChange
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, stepped)
	require.Equal(t, (300 * time.Millisecond).Nanoseconds(), estimate)
}
