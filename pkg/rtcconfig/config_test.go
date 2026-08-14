// Copyright 2026 LiveKit, Inc.
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

package rtcconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRTCConfigAdditionalHostIPs(t *testing.T) {
	var conf RTCConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
udp_port: 7882
node_ip: 192.0.2.10
additional_host_ips:
  - " 203.0.113.10 "
  - 203.0.113.10
  - 2001:0db8::10
  - 2001:db8::10
`), &conf))

	require.NoError(t, conf.Validate(false))
	require.Equal(t, []string{"203.0.113.10", "2001:db8::10"}, conf.AdditionalHostIPs)
}

func TestRTCConfigRejectsInvalidAdditionalHostIP(t *testing.T) {
	conf := RTCConfig{
		UDPPort:           PortRange{Start: 7882},
		NodeIP:            NodeIP{V4: "192.0.2.10"},
		AdditionalHostIPs: []string{"203.0.113.10/32"},
	}

	require.EqualError(t, conf.Validate(false), `invalid additional host ip "203.0.113.10/32"`)
}
