package tunnel

import (
	"fmt"
	"net"
	"strings"
)

type AllowList struct {
	ips  map[string]struct{}
	nets []*net.IPNet
}

func ParseAllowList(entries []string) (*AllowList, error) {
	list := &AllowList{ips: make(map[string]struct{})}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, ipNet, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("parse allowlist CIDR %q: %w", entry, err)
			}
			list.nets = append(list.nets, ipNet)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("parse allowlist IP %q", entry)
		}
		list.ips[ip.String()] = struct{}{}
	}
	return list, nil
}

func (l *AllowList) ContainsAddr(addr net.Addr) bool {
	if l == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if _, ok := l.ips[ip.String()]; ok {
		return true
	}
	for _, ipNet := range l.nets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}
