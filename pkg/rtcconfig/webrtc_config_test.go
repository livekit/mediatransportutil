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

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

func TestBuildNAT1To1Rules(t *testing.T) {
	tests := []struct {
		name          string
		ips           []string
		candidateType webrtc.ICECandidateType
		mode          webrtc.ICEAddressRewriteMode
		want          []webrtc.ICEAddressRewriteRule
	}{
		{
			name:          "replace host keeps legacy mapping shape",
			ips:           []string{"203.0.113.10/10.0.0.10", "198.51.100.20"},
			candidateType: webrtc.ICECandidateTypeHost,
			mode:          webrtc.ICEAddressRewriteReplace,
			want: []webrtc.ICEAddressRewriteRule{
				{
					External:        []string{"203.0.113.10"},
					Local:           "10.0.0.10",
					AsCandidateType: webrtc.ICECandidateTypeHost,
					Mode:            webrtc.ICEAddressRewriteReplace,
				},
				{
					External:        []string{"203.0.113.10", "198.51.100.20"},
					AsCandidateType: webrtc.ICECandidateTypeHost,
					Mode:            webrtc.ICEAddressRewriteReplace,
				},
			},
		},
		{
			name:          "append srflx emits explicit and catch-all",
			ips:           []string{"203.0.113.10/10.0.0.10"},
			candidateType: webrtc.ICECandidateTypeSrflx,
			mode:          webrtc.ICEAddressRewriteAppend,
			want: []webrtc.ICEAddressRewriteRule{
				{
					External:        []string{"203.0.113.10"},
					Local:           "10.0.0.10",
					AsCandidateType: webrtc.ICECandidateTypeSrflx,
					Mode:            webrtc.ICEAddressRewriteAppend,
				},
				{
					External:        []string{"203.0.113.10"},
					AsCandidateType: webrtc.ICECandidateTypeSrflx,
					Mode:            webrtc.ICEAddressRewriteAppend,
				},
			},
		},
		{
			name:          "append srflx skips self mappings but keeps real external in catch-all",
			ips:           []string{"203.0.113.10/10.0.0.10", "10.0.0.20/10.0.0.20"},
			candidateType: webrtc.ICECandidateTypeSrflx,
			mode:          webrtc.ICEAddressRewriteAppend,
			want: []webrtc.ICEAddressRewriteRule{
				{
					External:        []string{"203.0.113.10"},
					Local:           "10.0.0.10",
					AsCandidateType: webrtc.ICECandidateTypeSrflx,
					Mode:            webrtc.ICEAddressRewriteAppend,
				},
				{
					External:        []string{"203.0.113.10"},
					AsCandidateType: webrtc.ICECandidateTypeSrflx,
					Mode:            webrtc.ICEAddressRewriteAppend,
				},
			},
		},
		{
			name:          "append srflx keeps unmapped external ips as catch-all",
			ips:           []string{"198.51.100.20"},
			candidateType: webrtc.ICECandidateTypeSrflx,
			mode:          webrtc.ICEAddressRewriteAppend,
			want: []webrtc.ICEAddressRewriteRule{
				{
					External:        []string{"198.51.100.20"},
					AsCandidateType: webrtc.ICECandidateTypeSrflx,
					Mode:            webrtc.ICEAddressRewriteAppend,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNAT1To1Rules(tt.ips, tt.candidateType, tt.mode)
			require.Equal(t, tt.want, got)
			for _, rule := range got {
				require.Nil(t, rule.Networks)
			}
		})
	}
}

func TestChooseNAT1To1Mode(t *testing.T) {
	tests := []struct {
		name                 string
		advertisePrivateIPs  bool
		externalIPOnly       bool
		externalMappingCount int
		wantMode             webrtc.ICEAddressRewriteMode
		wantCandidateType    webrtc.ICECandidateType
	}{
		{
			name:              "advertise private ips disabled",
			wantMode:          webrtc.ICEAddressRewriteReplace,
			wantCandidateType: webrtc.ICECandidateTypeHost,
		},
		{
			name:                 "external only wins",
			advertisePrivateIPs:  true,
			externalIPOnly:       true,
			externalMappingCount: 1,
			wantMode:             webrtc.ICEAddressRewriteReplace,
			wantCandidateType:    webrtc.ICECandidateTypeHost,
		},
		{
			name:                "no mappings falls back to replace",
			advertisePrivateIPs: true,
			wantMode:            webrtc.ICEAddressRewriteReplace,
			wantCandidateType:   webrtc.ICECandidateTypeHost,
		},
		{
			name:                 "advertise private ips appends srflx",
			advertisePrivateIPs:  true,
			externalMappingCount: 1,
			wantMode:             webrtc.ICEAddressRewriteAppend,
			wantCandidateType:    webrtc.ICECandidateTypeSrflx,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotCandidateType := chooseNAT1To1Mode(tt.advertisePrivateIPs, tt.externalIPOnly, tt.externalMappingCount)
			require.Equal(t, tt.wantMode, gotMode)
			require.Equal(t, tt.wantCandidateType, gotCandidateType)
		})
	}
}

func TestRTCConfig_ValidateAdvertisePrivateIPs(t *testing.T) {
	// Tests target validateAdvertisePrivateIPs directly so they don't need to
	// go through Validate's full flow (which calls determineIP and reaches
	// the network when UseExternalIP is true).
	tests := []struct {
		name      string
		conf      RTCConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "advertise_private_ips disabled is always allowed",
			conf: RTCConfig{
				AdvertisePrivateIPs: false,
				ForceTCP:            true,
				UDPPort:             PortRange{Start: 7882},
			},
		},
		{
			name: "advertise_private_ips with use_external_ip=false is allowed (flag has no effect)",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       false,
				ForceTCP:            true,
				UDPPort:             PortRange{Start: 7882},
			},
		},
		{
			name: "advertise_private_ips with external_ip_only=true is allowed (flag has no effect)",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ExternalIPOnly:      true,
				ForceTCP:            true,
				UDPPort:             PortRange{Start: 7882},
			},
		},
		{
			name: "advertise_private_ips with force_tcp is rejected",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ForceTCP:            true,
				ICEPortRangeStart:   50000,
				ICEPortRangeEnd:     60000,
			},
			wantErr:   true,
			errSubstr: "force_tcp",
		},
		{
			name: "advertise_private_ips with single-port udp_port (no port range) is rejected",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				UDPPort:             PortRange{Start: 7882},
			},
			wantErr:   true,
			errSubstr: "port_range_start",
		},
		{
			name: "advertise_private_ips with partial port range (only start) plus udp_port is rejected",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ICEPortRangeStart:   50000,
				// ICEPortRangeEnd intentionally left zero — operator typo
				UDPPort: PortRange{Start: 7882},
			},
			wantErr:   true,
			errSubstr: "port_range_start",
		},
		{
			name: "advertise_private_ips with partial port range (only end) plus udp_port is rejected",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				// ICEPortRangeStart intentionally left zero
				ICEPortRangeEnd: 60000,
				UDPPort:         PortRange{Start: 7882},
			},
			wantErr:   true,
			errSubstr: "port_range_start",
		},
		{
			name: "advertise_private_ips with partial port range and no udp_port is rejected",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ICEPortRangeStart:   50000,
				// ICEPortRangeEnd intentionally left zero, no udp_port either
			},
			wantErr:   true,
			errSubstr: "port_range_start",
		},
		{
			// Operators enabling advertise_private_ips must explicitly configure
			// the port range. Relying on Validate's defaulting logic would mask
			// end-only partial-range typos (defaulting only checks start==0 and
			// then overwrites end), so the gate must run before defaulting and
			// reject configurations that don't have an explicit port range.
			name: "advertise_private_ips with no port config at all is rejected (must be explicit)",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				// no port_range_*, no udp_port — pre-defaulting state
			},
			wantErr:   true,
			errSubstr: "port_range_start",
		},
		{
			name: "advertise_private_ips with port range only is allowed",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ICEPortRangeStart:   50000,
				ICEPortRangeEnd:     60000,
			},
		},
		{
			name: "advertise_private_ips with port range and udp_port (port range wins) is allowed",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ICEPortRangeStart:   50000,
				ICEPortRangeEnd:     60000,
				UDPPort:             PortRange{Start: 7882},
			},
		},
		{
			name: "force_tcp gate fires before port_range gate when both invalid",
			conf: RTCConfig{
				AdvertisePrivateIPs: true,
				UseExternalIP:       true,
				ForceTCP:            true,
				UDPPort:             PortRange{Start: 7882},
			},
			wantErr:   true,
			errSubstr: "force_tcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.conf.validateAdvertisePrivateIPs()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
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
