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

package sdp

import (
	"strings"

	"github.com/livekit/mediatransportutil/pkg/audio"
)

var (
	codecByName = make(map[string]audio.Codec)
)

func init() {
	audio.OnRegister(func(c audio.Codec) {
		name := c.Info().SDPName
		if name != "" {
			name = strings.ToLower(name)
			codecByName[name] = c
			if strings.Count(name, "/") == 1 {
				codecByName[name+"/1"] = c
			}
		}
	})
}

func CodecByName(name string) audio.Codec {
	c := codecByName[strings.ToLower(name)]
	if !audio.CodecEnabled(c) {
		return nil
	}
	return c
}
