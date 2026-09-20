package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"
)

// scanSubnet probes the RTSP port on every host of cfg.Subnet and returns
// the addresses that answered, sorted.
func scanSubnet(cfg Config) ([]string, error) {
	hosts, err := subnetHosts(cfg.Subnet)
	if err != nil {
		return nil, err
	}

	jobs := make(chan netip.Addr)
	results := make(chan netip.Addr, len(hosts))

	var wg sync.WaitGroup
	for range cfg.ScanWorkers {
		wg.Go(func() {
			for addr := range jobs {
				if rtspReachable(addr, cfg.RTSPPort, cfg.ProbeTimeout) {
					results <- addr
				}
			}
		})
	}
	for _, addr := range hosts {
		jobs <- addr
	}
	close(jobs)
	wg.Wait()
	close(results)

	var found []netip.Addr
	for addr := range results {
		found = append(found, addr)
	}
	slices.SortFunc(found, netip.Addr.Compare)

	ips := make([]string, len(found))
	for i, addr := range found {
		ips[i] = addr.String()
	}
	return ips, nil
}

// subnetHosts enumerates the usable addresses of an IPv4 CIDR, skipping
// the network and broadcast addresses.
func subnetHosts(cidr string) ([]netip.Addr, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %q: %w", cidr, err)
	}
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("only IPv4 subnets are supported, got %q", cidr)
	}
	prefix = prefix.Masked()
	ones := prefix.Bits()
	if ones < 16 || ones > 30 {
		return nil, fmt.Errorf("subnet /%d is out of the supported /16../30 range", ones)
	}

	addr4 := prefix.Addr().As4()
	base := binary.BigEndian.Uint32(addr4[:])
	size := uint64(1) << (32 - ones)
	hosts := make([]netip.Addr, 0, size-2)
	for i := uint64(1); i < size-1; i++ {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], base+uint32(i))
		hosts = append(hosts, netip.AddrFrom4(b))
	}
	return hosts, nil
}

// rtspReachable reports whether the TCP port answers; an open RTSP port
// marks a likely camera and the ffmpeg run makes the final call.
func rtspReachable(addr netip.Addr, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr.String(), strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
