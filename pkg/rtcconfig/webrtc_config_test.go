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

package rtcconfig

import (
	"net"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/transport/v4/vnet"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

func TestSetNAT1To1AddressRewriteRulesCandidateModes(t *testing.T) {
	const (
		localIP          = "10.0.0.10"
		primaryHostIP    = "192.0.2.10"
		additionalHostIP = "198.51.100.10"
	)

	for _, test := range []struct {
		name            string
		includeInternal bool
	}{
		{name: "replace", includeInternal: false},
		{name: "append", includeInternal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, err := vnet.NewRouter(&vnet.RouterConfig{
				CIDR:          "10.0.0.0/24",
				LoggerFactory: logging.NewDefaultLoggerFactory(),
			})
			require.NoError(t, err)
			network, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{localIP}})
			require.NoError(t, err)
			require.NoError(t, router.AddNet(network))
			require.NoError(t, router.Start())
			t.Cleanup(func() { require.NoError(t, router.Stop()) })

			settingEngine := webrtc.SettingEngine{}
			settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
			settingEngine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
			settingEngine.SetNet(network)
			require.NoError(t, SetNAT1To1AddressRewriteRules(
				&settingEngine,
				[]string{primaryHostIP, additionalHostIP},
				test.includeInternal,
			))

			hostAddresses := gatherHostCandidateAddresses(t, settingEngine)
			require.Contains(t, hostAddresses, primaryHostIP)
			require.Contains(t, hostAddresses, additionalHostIP)
			if test.includeInternal {
				require.Contains(t, hostAddresses, localIP)
			} else {
				require.NotContains(t, hostAddresses, localIP)
			}
		})
	}
}

func gatherHostCandidateAddresses(t *testing.T, settingEngine webrtc.SettingEngine) []string {
	t.Helper()

	gatherer, err := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine)).NewICEGatherer(webrtc.ICEGatherOptions{})
	require.NoError(t, err)
	done := make(chan struct{})
	var addresses []string
	gatherer.OnLocalCandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			close(done)
			return
		}
		if candidate.Typ == webrtc.ICECandidateTypeHost {
			addresses = append(addresses, candidate.Address)
		}
	})

	require.NoError(t, gatherer.Gather())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ICE gathering did not complete")
	}
	require.NoError(t, gatherer.Close())
	return addresses
}

func TestNAT1To1AddressRewriteRules(t *testing.T) {
	rules := nat1To1AddressRewriteRules([]string{
		"198.51.100.10/10.0.0.10",
		"198.51.100.11/10.0.0.10",
		"203.0.113.10",
		"203.0.113.10",
	}, true)

	require.Equal(t, []webrtc.ICEAddressRewriteRule{
		{
			External:        []string{"198.51.100.10", "198.51.100.11"},
			Local:           "10.0.0.10",
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteAppend,
		},
		{
			External:        []string{"203.0.113.10"},
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteAppend,
		},
	}, rules)
}

func TestWithAdditionalHostIPs(t *testing.T) {
	ips := []string{
		"198.51.100.10/10.0.0.10",
		"2001:db8::10/fd00::10",
	}
	additionalHostIPs := []string{
		"203.0.113.10",
		"203.0.113.11",
		"2001:db8::20",
		"198.51.100.10",
	}

	require.Equal(t, []string{
		"198.51.100.10/10.0.0.10",
		"2001:db8::10/fd00::10",
		"203.0.113.10/10.0.0.10",
		"203.0.113.11/10.0.0.10",
		"2001:db8::20/fd00::10",
		"203.0.113.10",
		"203.0.113.11",
		"2001:db8::20",
		"198.51.100.10",
	}, withAdditionalHostIPs(ips, additionalHostIPs))
}

func TestWithAdditionalHostIPsWithoutMappings(t *testing.T) {
	require.Equal(t,
		[]string{"192.0.2.10", "198.51.100.10"},
		withAdditionalHostIPs(
			[]string{"192.0.2.10"},
			[]string{"198.51.100.10", "192.0.2.10"},
		),
	)
}

func Test_IPFilterFromConf(t *testing.T) {
	testData := IPsConfig{
		Includes: []string{"10.0.0.0/19"},
		Excludes: []string{"10.0.0.0/9", "10.192.0.0/11", "10.244.0.0/16", "172.16.0.0/12", "192.168.128.0/17"},
	}

	ipFilter, err := IPFilterFromConf(testData)
	require.NoError(t, err)

	testCases := []struct {
		ip       string
		expected bool
	}{
		{"10.0.0.10", true},
		{"10.0.0.1", true},
		{"10.0.31.255", true},
		{"10.0.32.1", false},
		{"10.192.0.1", false},
		{"10.244.0.1", false},
		{"172.16.0.10", false},
		{"192.168.128.5", false},
	}

	for _, tc := range testCases {
		testIP := net.ParseIP(tc.ip)

		if result := ipFilter(testIP); result != tc.expected {
			t.Errorf("For IP %s, expected %v but got %v", tc.ip, tc.expected, result)
		}
	}

	testData = IPsConfig{
		Includes: []string{"192.168.128.1"},
		Excludes: []string{"192.168.128.0/17"},
	}
	_, err = IPFilterFromConf(testData)
	require.Error(t, err)
}

func Test_InterfaceFilterFromConf(t *testing.T) {
	testData := InterfacesConfig{
		Includes: []string{"eth0", "eth1", "eth2"},
		Excludes: []string{"eth0", "eth3", "eth4"},
	}

	ifaceFilter := InterfaceFilterFromConf(testData)

	testCases := []struct {
		iface    string
		expected bool
	}{
		{"eth0", true},
		{"eth1", true},
		{"eth2", true},
		{"eth3", false},
		{"eth4", false},
		{"eth5", false},
	}

	for _, tc := range testCases {
		if result := ifaceFilter(tc.iface); result != tc.expected {
			t.Errorf("For interface %s, expected %v but got %v", tc.iface, tc.expected, result)
		}
	}
}
