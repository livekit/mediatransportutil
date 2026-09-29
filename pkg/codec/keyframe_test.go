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

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsH265KeyFrameFragmentationUnit(t *testing.T) {
	// payload header for an FU (type 49), layer 0, TID 1
	fuHeader := []byte{49 << 1, 0x01}

	testCases := []struct {
		name     string
		fuHeader byte
		expected bool
	}{
		{"SPS first fragment", 0x80 | 33, true},
		{"PPS first fragment", 0x80 | 34, true},
		{"SPS continuation fragment", 33, false},
		{"SPS last fragment", 0x40 | 33, false},
		{"IDR first fragment", 0x80 | 19, false},
		// FuType 2 with the E bit set: bits 0x42 must not be read as type 33
		{"non parameter set with E bit", 0x40 | 2, false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := append(append([]byte{}, fuHeader...), tc.fuHeader, 0xAA, 0xBB)
			require.Equal(t, tc.expected, IsH265KeyFrame(payload))
			require.Equal(t, tc.expected, IsKeyFrame("h265", payload))
		})
	}
}
