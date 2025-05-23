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
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/livekit/protocol/logger"
)

const (
	// number of packets to buffer up
	readBufferSize = 50

	minUDPBufferSize       = 5_000_000
	writeBufferSizeInBytes = 4 * 1024 * 1024
	defaultUDPBufferSize   = 16_777_216
)

var DefaultStunServers = []string{
	"global.stun.twilio.com:3478",
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
}

type RTCConfig struct {
	UDPPort                 PortRange        `yaml:"udp_port,omitempty"`
	TCPPort                 uint32           `yaml:"tcp_port,omitempty"`
	ICEPortRangeStart       uint32           `yaml:"port_range_start,omitempty"`
	ICEPortRangeEnd         uint32           `yaml:"port_range_end,omitempty"`
	NodeIP                  string           `yaml:"node_ip,omitempty"`
	NodeIPAutoGenerated     bool             `yaml:"-"`
	STUNServers             []string         `yaml:"stun_servers,omitempty"`
	UseExternalIP           bool             `yaml:"use_external_ip"`
	UseICELite              bool             `yaml:"use_ice_lite,omitempty"`
	Interfaces              InterfacesConfig `yaml:"interfaces,omitempty"`
	IPs                     IPsConfig        `yaml:"ips,omitempty"`
	EnableLoopbackCandidate bool             `yaml:"enable_loopback_candidate"`
	UseMDNS                 bool             `yaml:"use_mdns,omitempty"`
	// when UseExternalIP is true, only advertise the external IP to client
	ExternalIPOnly bool          `yaml:"external_ip_only,omitempty"`
	BatchIO        BatchIOConfig `yaml:"batch_io,omitempty"`

	// SCTP congestion control
	SCTPMinCwnd    int `yaml:"sctp_min_cwnd,omitempty"`
	SCTPFastRtxWnd int `yaml:"sctp_fast_rtx_wnd,omitempty"`
	SCTPCwndCAStep int `yaml:"sctp_cwnd_ca_step,omitempty"`

	// for testing, disable UDP
	ForceTCP bool `yaml:"force_tcp,omitempty"`
}

type InterfacesConfig struct {
	Includes []string `yaml:"includes,omitempty"`
	Excludes []string `yaml:"excludes,omitempty"`
}

type IPsConfig struct {
	Includes []string `yaml:"includes,omitempty"`
	Excludes []string `yaml:"excludes,omitempty"`
}

type BatchIOConfig struct {
	BatchSize        int           `yaml:"batch_size,omitempty"`
	MaxFlushInterval time.Duration `yaml:"max_flush_interval,omitempty"`
}

func (conf *RTCConfig) Validate(development bool) error {
	// set defaults for ports if none are set
	if !conf.UDPPort.Valid() && conf.ICEPortRangeStart == 0 {
		// to make it easier to run in dev mode/docker, default to single port
		if development {
			conf.UDPPort = PortRange{Start: 7882}
		} else {
			conf.ICEPortRangeStart = 50000
			conf.ICEPortRangeEnd = 60000
		}
	}

	var err error
	if conf.NodeIP == "" || conf.UseExternalIP {
		conf.NodeIP, err = conf.determineIP()
		if err != nil {
			logger.Warnw("could not determine node ip", err)
			return err
		}
		conf.NodeIPAutoGenerated = true
	}

	return nil
}

type PortRange struct {
	Start int `yaml:"start,omitempty"`
	End   int `yaml:"end,omitempty"`
}

func (r PortRange) MarshalYAML() (interface{}, error) {
	if r.End == 0 {
		return r.Start, nil
	}

	if r.End <= r.Start {
		return nil, fmt.Errorf("end port %d must be greater than start port %d", r.End, r.Start)
	}
	return fmt.Sprintf("%d-%d", r.Start, r.End), nil
}

func (r *PortRange) UnmarshalYAML(value *yaml.Node) error {
	return r.UnmarshalString(value.Value)
}

func (r *PortRange) UnmarshalString(str string) error {
	if str == "" {
		return nil
	}
	if strings.Contains(str, "-") {
		parts := strings.Split(str, "-")
		if len(parts) != 2 {
			return fmt.Errorf("invalid port range %s, should be <start>-<end>", str)
		}
		start, err := strconv.Atoi(strings.Trim(parts[0], " "))
		if err != nil {
			return fmt.Errorf("invalid start port %s: %v", parts[0], err)
		}
		end, err := strconv.Atoi(strings.Trim(parts[1], " "))
		if err != nil {
			return fmt.Errorf("invalid end port %s: %v", parts[1], err)
		}
		if end <= start {
			return fmt.Errorf("end port %d must be greater than start port %d", end, start)
		}

		r.Start = start
		r.End = end
		return nil
	}

	port, err := strconv.Atoi(str)
	if err != nil {
		return fmt.Errorf("invalid port %s: %v", str, err)
	}
	r.Start = port
	return nil
}

func (r *PortRange) ToSlice() []int {
	if r.End == 0 || r.End <= r.Start {
		return []int{r.Start}
	}

	ports := make([]int, r.End-r.Start+1)
	for i := r.Start; i <= r.End; i++ {
		ports[i-r.Start] = i
	}
	return ports
}

func (r *PortRange) Valid() bool {
	return r.Start != 0
}
