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
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/pion/stun/v3"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRTCConfig_UnmarshalSkipExternalIPValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{name: "absent defaults to false", yaml: "use_external_ip: true\n", want: false},
		{name: "explicit true", yaml: "use_external_ip: true\nskip_external_ip_validation: true\n", want: true},
		{name: "explicit false", yaml: "use_external_ip: true\nskip_external_ip_validation: false\n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg RTCConfig
			require.NoError(t, yaml.Unmarshal([]byte(tt.yaml), &cfg))
			require.Equal(t, tt.want, cfg.SkipExternalIPValidation)
		})
	}
}

// mockSTUNServer answers a single STUN binding request with a fixed
// XOR-MAPPED-ADDRESS so findExternalIPWithOptions can be exercised without
// reaching out to real STUN providers in tests.
func mockSTUNServer(t *testing.T, mappedIP string, mappedPort int) (string, func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			select {
			case <-stop:
				return
			default:
			}
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			req := &stun.Message{Raw: append([]byte{}, buf[:n]...)}
			if err := req.Decode(); err != nil {
				continue
			}
			resp, err := stun.Build(
				stun.NewTransactionIDSetter(req.TransactionID),
				stun.BindingSuccess,
				&stun.XORMappedAddress{IP: net.ParseIP(mappedIP), Port: mappedPort},
				stun.Fingerprint,
			)
			if err != nil {
				continue
			}
			_, _ = conn.WriteToUDP(resp.Raw, src)
		}
	}()

	addr := conn.LocalAddr().(*net.UDPAddr)
	cleanup := func() {
		close(stop)
		conn.Close()
		<-done
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port)), cleanup
}

func TestFindExternalIPWithOptions_SkipValidation(t *testing.T) {
	stunAddr, cleanup := mockSTUNServer(t, "203.0.113.42", 50000)
	defer cleanup()

	// validateExternalIP short-circuits when localAddr is nil
	// (`if addr == nil { return nil }`), so to actually exercise the
	// skipValidation flag we must hand findExternalIPWithOptions a non-nil
	// local UDP address. The localAddr below is a valid socket on this host;
	// validateExternalIP will try to DialUDP from the (unreachable RFC-5737)
	// reflexive IP back to that local listener, which can never round-trip
	// in a unit-test environment, so skip=false hits the validationTimeout.

	t.Run("skip true returns STUN result without waiting for validation", func(t *testing.T) {
		localAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		start := time.Now()
		ip, err := findExternalIPWithOptions(ctx, stunAddr, localAddr, true)
		elapsed := time.Since(start)

		require.NoError(t, err)
		require.Equal(t, "203.0.113.42", ip)
		require.Less(t, elapsed, validationTimeout,
			"skip=true should not wait for the self-hairpin validation window")
	})

	t.Run("skip false runs validation and times out when self-hairpin is impossible", func(t *testing.T) {
		localAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
		ctx, cancel := context.WithTimeout(context.Background(), validationTimeout+2*time.Second)
		defer cancel()

		start := time.Now()
		_, err := findExternalIPWithOptions(ctx, stunAddr, localAddr, false)
		elapsed := time.Since(start)

		require.Error(t, err)
		require.GreaterOrEqual(t, elapsed, validationTimeout,
			"skip=false must run validateExternalIP, which cannot succeed here and times out")
	})
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
