package tunnel

import (
	"net"
	"testing"
)

func TestAllowListContainsIPAndCIDR(t *testing.T) {
	list, err := ParseAllowList([]string{"192.168.44.10", "fd00::/8"})
	if err != nil {
		t.Fatalf("ParseAllowList: %v", err)
	}
	cases := []struct {
		addr string
		want bool
	}{
		{"192.168.44.10:50000", true},
		{"192.168.44.11:50000", false},
		{"[fd00::1234]:50000", true},
		{"[2001:db8::1]:50000", false},
	}
	for _, tc := range cases {
		if got := list.ContainsAddr(&net.TCPAddr{IP: net.ParseIP(hostOnly(tc.addr)), Port: 50000}); got != tc.want {
			t.Fatalf("ContainsAddr(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func hostOnly(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	return host
}
