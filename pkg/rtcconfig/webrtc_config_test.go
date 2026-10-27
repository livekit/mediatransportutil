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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

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

func Test_NAT1To1AddressRewriteRules(t *testing.T) {
	t.Run("catchAll replaces host candidate when includeInternal is false", func(t *testing.T) {
		rules := nat1To1AddressRewriteRules([]string{"1.2.3.4"}, false)
		require.Len(t, rules, 1)
		require.Equal(t, []string{"1.2.3.4"}, rules[0].External)
		require.Equal(t, webrtc.ICECandidateTypeHost, rules[0].AsCandidateType)
		require.Equal(t, webrtc.ICEAddressRewriteModeUnspecified, rules[0].Mode)
	})

	t.Run("catchAll appends host candidate when includeInternal is true", func(t *testing.T) {
		rules := nat1To1AddressRewriteRules([]string{"1.2.3.4"}, true)
		require.Len(t, rules, 1)
		require.Equal(t, []string{"1.2.3.4"}, rules[0].External)
		require.Equal(t, webrtc.ICECandidateTypeHost, rules[0].AsCandidateType)
		require.Equal(t, webrtc.ICEAddressRewriteAppend, rules[0].Mode)
	})

	t.Run("external/local pairs and catchAll IPs get the same mode", func(t *testing.T) {
		rules := nat1To1AddressRewriteRules([]string{"5.6.7.8/10.0.0.1", "1.2.3.4"}, true)
		require.Len(t, rules, 2)

		require.Equal(t, []string{"5.6.7.8"}, rules[0].External)
		require.Equal(t, "10.0.0.1", rules[0].Local)
		require.Equal(t, webrtc.ICEAddressRewriteAppend, rules[0].Mode)

		require.Equal(t, []string{"1.2.3.4"}, rules[1].External)
		require.Equal(t, webrtc.ICEAddressRewriteAppend, rules[1].Mode)
	})
}

func Test_GetNAT1to1IPsForConf_Families(t *testing.T) {
	isLoopback := func(name string) bool { return strings.HasPrefix(name, "lo") }
	localIPs, err := GetLocalIPAddresses(true, true, isLoopback, nil)
	require.NoError(t, err)
	if !slices.Contains(localIPs, "127.0.0.1") || !slices.Contains(localIPs, "::1") {
		t.Skip("needs IPv4 and IPv6 loopback")
	}

	getIPs := func(t *testing.T, servers ...string) []string {
		conf := &RTCConfig{
			STUNServers:              servers,
			EnableLoopbackCandidate:  true,
			SkipExternalIPValidation: true,
			ExternalIPOnly:           true,
		}
		ips, ipFilter, err := getNAT1to1IPsForConf(conf, isLoopback, nil)
		require.NoError(t, err)
		require.True(t, ipFilter(net.ParseIP("127.0.0.1")))
		return ips
	}

	// the slow family answers 2s after the fast one, past the 1s grace period
	t.Run("slow IPv4", func(t *testing.T) {
		ips := getIPs(t,
			startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 2*time.Second, nil),
			startSTUNServer(t, "udp6", "[::1]:0", net.ParseIP("2001:db8::1"), 0, nil),
		)
		require.ElementsMatch(t, []string{"203.0.113.1/127.0.0.1", "2001:db8::1/::1"}, ips)
	})

	t.Run("slow IPv6", func(t *testing.T) {
		ips := getIPs(t,
			startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 0, nil),
			startSTUNServer(t, "udp6", "[::1]:0", net.ParseIP("2001:db8::1"), 2*time.Second, nil),
		)
		require.ElementsMatch(t, []string{"203.0.113.1/127.0.0.1", "2001:db8::1/::1"}, ips)
	})

	// IPv6 has no server, so its lookup fails at once and must not hold up the result
	t.Run("failed IPv6", func(t *testing.T) {
		start := time.Now()
		ips := getIPs(t, startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 0, nil))
		require.ElementsMatch(t, []string{"203.0.113.1/127.0.0.1", "::1/::1"}, ips)
		require.Less(t, time.Since(start), time.Second)
	})
}

// startSTUNServer answers binding requests with external as the mapped address, after
// delay. When sources is not nil, it gets the source address of each request.
func startSTUNServer(t *testing.T, network, addr string, external net.IP, delay time.Duration, sources chan<- net.Addr) string {
	conn, err := net.ListenPacket(network, addr)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			req := &stun.Message{Raw: slices.Clone(buf[:n])}
			if err := req.Decode(); err != nil {
				continue
			}
			if sources != nil {
				sources <- from
			}
			res, err := stun.Build(
				stun.NewTransactionIDSetter(req.TransactionID),
				stun.BindingSuccess,
				&stun.XORMappedAddress{IP: external, Port: from.(*net.UDPAddr).Port},
			)
			if err != nil {
				continue
			}
			time.AfterFunc(delay, func() { _, _ = conn.WriteTo(res.Raw, from) })
		}
	}()
	return conn.LocalAddr().String()
}
