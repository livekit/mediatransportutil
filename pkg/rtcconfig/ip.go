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
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/pion/stun/v3"

	"github.com/livekit/protocol/logger"
)

const (
	stunPingTimeout   = 5 * time.Second
	validationTimeout = 5 * time.Second
)

func (conf *RTCConfig) determineIP() (string, error) {
	if conf.UseExternalIP {
		stunServers := conf.STUNServers
		if len(stunServers) == 0 {
			stunServers = DefaultStunServers
		}
		// When an IPv4 is required, dial STUN over udp4 so a not-yet-attached IPv4
		// surfaces as an error (and is retried) instead of the request silently
		// resolving the node's IPv6.
		getIP := GetExternalIP
		if conf.RequireIPv4 {
			getIP = GetExternalIPv4
		}
		var err error
		for range 3 {
			var ip string
			ip, err = getIP(context.Background(), stunServers, nil)
			if err == nil {
				return ip, nil
			} else {
				time.Sleep(500 * time.Millisecond)
			}
		}
		logger.Warnw("could not resolve external IP", err)
		return "", fmt.Errorf("could not resolve external IP: %w", err)
	}

	// use local ip instead
	addresses, err := GetLocalIPAddresses(false, true, nil, nil)
	// prefer IPv4 address if available
	for _, addr := range addresses {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			return addr, err
		}
	}
	if conf.RequireIPv4 {
		return "", fmt.Errorf("no IPv4 address available")
	}
	if len(addresses) > 0 {
		return addresses[0], err
	}
	return "", err
}

func GetLocalIPAddresses(includeLoopback bool, includeV6 bool, ifFilter func(string) bool, ipFilter func(net.IP) bool) ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	loopBacks := make([]string, 0)
	addresses := make([]string, 0)
	for _, iface := range ifaces {
		if ifFilter != nil && !ifFilter(iface.Name) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch typedAddr := addr.(type) {
			case *net.IPNet:
				if !includeV6 {
					ip = typedAddr.IP.To4()
				} else {
					ip = typedAddr.IP
				}
			case *net.IPAddr:
				if !includeV6 {
					ip = typedAddr.IP.To4()
				} else {
					ip = typedAddr.IP
				}
			default:
				continue
			}
			if ip == nil {
				continue
			}
			if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if ipFilter != nil && !ipFilter(ip) {
				continue
			}
			if ip.IsLoopback() {
				loopBacks = append(loopBacks, ip.String())
			} else {
				addresses = append(addresses, ip.String())
			}
		}
	}

	if includeLoopback {
		addresses = append(addresses, loopBacks...)
	}

	if len(addresses) > 0 {
		return addresses, nil
	}
	if len(loopBacks) > 0 {
		return loopBacks, nil
	}
	return nil, fmt.Errorf("could not find local IP address")
}

type stunRequest struct {
	server string
	addr   *net.UDPAddr
	raw    []byte
}

// findExternalIP sends a binding request to every STUN server from one socket bound
// to localAddr, so the configured port is the one that is tested and a slow or down
// server does not hold up the others. It returns the first mapped address. Without a
// local address both families are queried and an IPv4 answer is preferred.
func findExternalIP(ctx context.Context, stunServers []string, localAddr net.Addr, network string) (string, error) {
	ctx1, cancel1 := context.WithTimeout(ctx, stunPingTimeout)
	defer cancel1()

	var laddr *net.UDPAddr
	if localAddr != nil {
		udpAddr, ok := localAddr.(*net.UDPAddr)
		if !ok {
			return "", errors.New("not UDP address")
		}
		laddr = udpAddr
	}
	// "udp", "udp4", "udp6" -> "ip", "ip4", "ip6", narrowed to the local address family
	ipNetwork := "ip" + strings.TrimPrefix(network, "udp")
	if ipNetwork == "ip" && laddr != nil && laddr.IP != nil {
		if laddr.IP.To4() != nil {
			ipNetwork = "ip4"
		} else {
			ipNetwork = "ip6"
		}
	}

	conn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	// unblocks a pending read on timeout or cancellation
	stop := context.AfterFunc(ctx1, func() { conn.Close() })
	defer stop()

	var errs []error
	requests := make(map[[stun.TransactionIDSize]byte]stunRequest)
	for _, ss := range stunServers {
		host, portStr, err := net.SplitHostPort(ss)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		port, err := net.LookupPort("udp", portStr)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx1, ipNetwork, host)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// one request per address family the server has
		for _, is4 := range []bool{true, false} {
			i := slices.IndexFunc(ips, func(ip netip.Addr) bool { return ip.Unmap().Is4() == is4 })
			if i < 0 {
				continue
			}
			msg, err := stun.Build(stun.TransactionID, stun.BindingRequest)
			if err != nil {
				return "", err
			}
			requests[msg.TransactionID] = stunRequest{
				server: ss,
				addr:   net.UDPAddrFromAddrPort(netip.AddrPortFrom(ips[i].Unmap(), uint16(port))),
				raw:    msg.Raw,
			}
		}
	}

	preferIPv4 := ipNetwork == "ip"
	var fallbackIP string
	buf := make([]byte, 1500)
	// retransmit unanswered requests with a doubling interval, like RFC 5389
	for rto := 500 * time.Millisecond; len(requests) > 0; rto *= 2 {
		for id, r := range requests {
			if _, err := conn.WriteToUDP(r.raw, r.addr); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.server, err))
				delete(requests, id)
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(rto))
		for len(requests) > 0 {
			n, _, err := conn.ReadFromUDP(buf)
			if ctx1.Err() != nil {
				if fallbackIP != "" {
					return fallbackIP, nil
				}
				return "", errors.Join(append(errs, ctx1.Err())...)
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				break
			}
			if err != nil {
				return "", err
			}

			msg := &stun.Message{Raw: buf[:n]}
			if err := msg.Decode(); err != nil {
				continue
			}
			r, ok := requests[msg.TransactionID]
			if !ok {
				continue
			}
			delete(requests, msg.TransactionID)

			var xorAddr stun.XORMappedAddress
			if err := xorAddr.GetFrom(msg); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.server, err))
				continue
			}
			ipAddr := xorAddr.IP.String()
			logger.Infow(
				"found external IP via STUN",
				"localAddr", localAddr,
				"stunServer", r.server,
				"externalIP", ipAddr,
			)
			if preferIPv4 && xorAddr.IP.To4() == nil {
				if fallbackIP == "" {
					fallbackIP = ipAddr
				}
				continue
			}
			return ipAddr, nil
		}
	}

	if fallbackIP != "" {
		return fallbackIP, nil
	}
	return "", errors.Join(errs...)
}

// GetExternalIP return external IP for localAddr from stun server. If localAddr is nil, a local address is chosen automatically,
// else the address will be used to validate the external IP is accessible from the outside.
func GetExternalIP(ctx context.Context, stunServers []string, localAddr net.Addr) (string, error) {
	return getExternalIP(ctx, stunServers, localAddr, true, "udp")
}

// GetExternalIPv4 is like GetExternalIP but forces the STUN request over IPv4, so
// it returns an IPv4 reflexive address (or an error if no IPv4 route exists).
func GetExternalIPv4(ctx context.Context, stunServers []string, localAddr net.Addr) (string, error) {
	return getExternalIP(ctx, stunServers, localAddr, true, "udp4")
}

func getExternalIP(ctx context.Context, stunServers []string, localAddr net.Addr, validate bool, network string) (string, error) {
	if len(stunServers) == 0 {
		return "", errors.New("STUN servers are required but not defined")
	}

	ipAddr, err := findExternalIP(ctx, stunServers, localAddr, network)
	if err != nil || !validate {
		return ipAddr, err
	}
	return ipAddr, validateExternalIP(ctx, ipAddr, localAddr)
}

// validateExternalIP validates that the external IP is accessible from the outside by listen the local address,
// it will send a magic string to the external IP and check the string is received by the local address.
func validateExternalIP(ctx context.Context, nodeIP string, addr net.Addr) error {
	if addr == nil {
		return nil
	}

	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return errors.New("not UDP address")
	}

	srv, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	defer srv.Close()

	magicString := "9#B8D2Nvg2xg5P$ZRwJ+f)*^Nne6*W3WamGY"

	validCh := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := srv.Read(buf)
			if err != nil {
				logger.Debugw("error reading from UDP socket", "err", err)
				return
			}
			if string(buf[:n]) == magicString {
				close(validCh)
				return
			}
		}
	}()

	cli, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(nodeIP), Port: srv.LocalAddr().(*net.UDPAddr).Port})
	if err != nil {
		return err
	}
	defer cli.Close()

	if _, err = cli.Write([]byte(magicString)); err != nil {
		return err
	}

	ctx1, cancel1 := context.WithTimeout(ctx, validationTimeout)
	defer cancel1()
	select {
	case <-validCh:
		return nil
	case <-ctx1.Done():
		logger.Warnw("could not validate external IP", ctx1.Err(), "ip", nodeIP, "from", addr)
		return ctx1.Err()
	}
}
