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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
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
	done   chan struct{}
}

// findExternalIP sends a binding request to every STUN server from one socket bound
// to localAddr, so the configured port is the one that is tested. Each server is
// resolved and queried on its own, so a slow lookup or a down server does not hold
// up the others. It returns the first mapped address. Without a local address both
// families are queried and an IPv4 answer is preferred.
//
// With validate and a local address, each distinct mapped address is probed by
// sending a packet to it that must come back to the socket, and the first address
// whose probe returns wins. Servers can map the socket to different addresses, for
// example on a multi-homed node, and only some of them may reach back.
func findExternalIP(ctx context.Context, stunServers []string, localAddr net.Addr, validate bool, network string) (string, error) {
	var laddr *net.UDPAddr
	if localAddr != nil {
		udpAddr, ok := localAddr.(*net.UDPAddr)
		if !ok {
			return "", errors.New("not UDP address")
		}
		laddr = udpAddr
	}
	validate = validate && laddr != nil

	// the socket outlives the STUN requests so that a late answer gets a full validation window
	timeout := stunPingTimeout
	if validate {
		timeout += validationTimeout
	}
	ctx1, cancel1 := context.WithTimeout(ctx, timeout)
	defer cancel1()
	stunCtx, stunCancel := context.WithTimeout(ctx1, stunPingTimeout)
	defer stunCancel()
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
	port := conn.LocalAddr().(*net.UDPAddr).Port
	// unblocks the read on timeout or cancellation
	stop := context.AfterFunc(ctx1, func() { conn.Close() })
	defer stop()

	var mu sync.Mutex
	var errs []error
	pending := make(map[[stun.TransactionIDSize]byte]stunRequest)
	addErr := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	// query sends a request and sends it again with a doubling interval, like RFC 5389,
	// until it is answered or the context ends
	query := func(server string, addr *net.UDPAddr) {
		defer wg.Done()
		msg, err := stun.Build(stun.TransactionID, stun.BindingRequest)
		if err != nil {
			addErr(err)
			return
		}
		done := make(chan struct{})
		mu.Lock()
		pending[msg.TransactionID] = stunRequest{server: server, done: done}
		mu.Unlock()
		// runs before wg.Done, so a pending entry means this query still holds a count
		defer func() {
			mu.Lock()
			delete(pending, msg.TransactionID)
			mu.Unlock()
		}()

		for rto := 500 * time.Millisecond; ; rto *= 2 {
			if _, err := conn.WriteToUDP(msg.Raw, addr); err != nil {
				addErr(fmt.Errorf("%s: %w", server, err))
				return
			}
			select {
			case <-done:
				return
			case <-stunCtx.Done():
				return
			case <-time.After(rto):
			}
		}
	}
	// probe sends the validation packet to ip and waits for the read below to see it
	// come back, which ends the call
	probe := func(ip string) {
		defer wg.Done()
		pctx, pcancel := context.WithTimeout(ctx1, validationTimeout)
		defer pcancel()

		err := sendProbe(ip, port)
		if err == nil {
			<-pctx.Done()
			if !errors.Is(pctx.Err(), context.DeadlineExceeded) {
				// validated or cancelled
				return
			}
			err = pctx.Err()
		}
		logger.Warnw("could not validate external IP", err, "ip", ip, "from", localAddr)
		addErr(fmt.Errorf("%s: %w", ip, err))
	}
	for _, ss := range stunServers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addrs, err := resolveSTUNServer(stunCtx, ss, ipNetwork)
			if err != nil {
				addErr(err)
				return
			}
			for _, addr := range addrs {
				wg.Add(1)
				go query(ss, addr)
			}
		}()
	}
	// wakes the read below once every lookup and request has ended
	go func() {
		wg.Wait()
		_ = conn.SetReadDeadline(time.Now())
	}()

	preferIPv4 := ipNetwork == "ip"
	var fallbackIP string
	probed := make(map[string]bool)
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if fallbackIP != "" {
				return fallbackIP, nil
			}
			mu.Lock()
			defer mu.Unlock()
			var netErr net.Error
			switch {
			case ctx1.Err() != nil:
				errs = append(errs, ctx1.Err())
			case !errors.As(err, &netErr) || !netErr.Timeout():
				errs = append(errs, err)
			case len(errs) == 0:
				errs = append(errs, errors.New("no STUN server answered"))
			}
			return "", errors.Join(errs...)
		}

		if ip, ok := bytes.CutPrefix(buf[:n], []byte(probeMagic)); ok {
			if probed[string(ip)] {
				return string(ip), nil
			}
			continue
		}

		msg := &stun.Message{Raw: buf[:n]}
		if err := msg.Decode(); err != nil {
			continue
		}
		var xorAddr stun.XORMappedAddress
		err = xorAddr.GetFrom(msg)
		ipAddr := xorAddr.IP.String()

		mu.Lock()
		r, ok := pending[msg.TransactionID]
		delete(pending, msg.TransactionID)
		// the entry's query still holds a count, so this Add cannot follow a finished Wait
		startProbe := ok && err == nil && validate && !probed[ipAddr]
		if startProbe {
			probed[ipAddr] = true
			wg.Add(1)
		}
		mu.Unlock()
		if !ok {
			continue
		}
		close(r.done)
		if startProbe {
			go probe(ipAddr)
		}
		if err != nil {
			addErr(fmt.Errorf("%s: %w", r.server, err))
			continue
		}
		logger.Infow(
			"found external IP via STUN",
			"localAddr", localAddr,
			"stunServer", r.server,
			"externalIP", ipAddr,
		)
		if validate {
			continue
		}
		if preferIPv4 && xorAddr.IP.To4() == nil {
			if fallbackIP == "" {
				fallbackIP = ipAddr
			}
			continue
		}
		return ipAddr, nil
	}
}

// resolveSTUNServer returns the first address of each family that the server has.
func resolveSTUNServer(ctx context.Context, server, ipNetwork string) ([]*net.UDPAddr, error) {
	host, portStr, err := net.SplitHostPort(server)
	if err != nil {
		return nil, err
	}
	port, err := net.LookupPort("udp", portStr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, ipNetwork, host)
	if err != nil {
		return nil, err
	}
	var addrs []*net.UDPAddr
	for _, is4 := range []bool{true, false} {
		if i := slices.IndexFunc(ips, func(ip netip.Addr) bool { return ip.Unmap().Is4() == is4 }); i >= 0 {
			addrs = append(addrs, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ips[i].Unmap(), uint16(port))))
		}
	}
	return addrs, nil
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

	return findExternalIP(ctx, stunServers, localAddr, validate, network)
}

// probeMagic starts the validation packet, which then names the external IP it was sent to.
const probeMagic = "9#B8D2Nvg2xg5P$ZRwJ+f)*^Nne6*W3WamGY"

// sendProbe sends the validation packet to the external IP from a separate socket, so it
// reaches the bound socket only if the external IP routes back to it.
func sendProbe(nodeIP string, port int) error {
	cli, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(nodeIP), Port: port})
	if err != nil {
		return err
	}
	defer cli.Close()

	_, err = cli.Write([]byte(probeMagic + nodeIP))
	return err
}
