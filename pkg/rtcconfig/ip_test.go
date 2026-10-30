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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_GetExternalIP_DownServer(t *testing.T) {
	silent, err := net.ListenPacket("udp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { silent.Close() })

	servers := []string{
		silent.LocalAddr().String(),
		startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 0, nil),
	}
	start := time.Now()
	ip, err := GetExternalIPv4(context.Background(), servers, nil)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.1", ip)
	require.Less(t, time.Since(start), stunPingTimeout)
}

func Test_GetExternalIP_PrefersIPv4(t *testing.T) {
	if conn, err := net.ListenPacket("udp6", "[::1]:0"); err != nil {
		t.Skip("needs IPv6 loopback")
	} else {
		conn.Close()
	}

	servers := []string{
		startSTUNServer(t, "udp6", "[::1]:0", net.ParseIP("2001:db8::1"), 0, nil),
		startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 300*time.Millisecond, nil),
	}
	ip, err := GetExternalIP(context.Background(), servers, nil)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.1", ip)
}

func Test_GetExternalIP_ConfiguredPort(t *testing.T) {
	// the server maps to loopback so that validation can reach the configured port
	sources := make(chan net.Addr, 16)
	servers := []string{
		startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("127.0.0.1"), 0, sources),
		startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("127.0.0.1"), 0, sources),
	}
	localAddr := freeUDPAddr(t)

	ip, err := GetExternalIP(context.Background(), servers, localAddr)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", ip)
	require.Equal(t, localAddr.Port, (<-sources).(*net.UDPAddr).Port)
}

func Test_GetExternalIP_ValidatesEachCandidate(t *testing.T) {
	// the servers map the socket to different addresses, like a multi-homed node, and
	// only the slower answer routes back to the socket
	servers := []string{
		startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 0, nil),
		startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("127.0.0.1"), 300*time.Millisecond, nil),
	}
	start := time.Now()
	ip, err := GetExternalIP(context.Background(), servers, freeUDPAddr(t))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", ip)
	require.Less(t, time.Since(start), validationTimeout)
}

func Test_GetExternalIP_NoCandidateValidates(t *testing.T) {
	// 203.0.113.1 is TEST-NET-3, so the probe never comes back
	servers := []string{startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("203.0.113.1"), 0, nil)}
	start := time.Now()
	_, err := GetExternalIP(context.Background(), servers, freeUDPAddr(t))
	require.ErrorContains(t, err, "203.0.113.1")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// a failed probe must end the call at its own timeout, not at the socket's
	require.Less(t, time.Since(start), validationTimeout+time.Second)
}

func Test_GetExternalIP_PortInUse(t *testing.T) {
	localAddr := freeUDPAddr(t)
	holder, err := net.ListenUDP("udp4", localAddr)
	require.NoError(t, err)
	t.Cleanup(func() { holder.Close() })

	servers := []string{startSTUNServer(t, "udp4", "127.0.0.1:0", net.ParseIP("127.0.0.1"), 0, nil)}
	_, err = GetExternalIP(context.Background(), servers, localAddr)
	// getNAT1to1IPsForConf moves to the next port on this error
	require.ErrorContains(t, err, "address already in use")
}

func freeUDPAddr(t *testing.T) *net.UDPAddr {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	addr := conn.LocalAddr().(*net.UDPAddr)
	require.NoError(t, conn.Close())
	return addr
}
