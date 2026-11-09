package proxy

import (
	"net/netip"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"1.1.1.1", true}, {"8.8.8.8", true}, {"2606:4700:4700::1111", true},
		{"127.0.0.1", false}, {"10.1.2.3", false}, {"192.168.1.1", false},
		{"169.254.1.1", false}, {"100.64.0.1", false}, {"198.18.0.1", false},
		{"192.0.2.1", false}, {"203.0.113.1", false}, {"::1", false},
		{"fc00::1", false}, {"fe80::1", false}, {"2001:db8::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			if got := isPublicIP(netip.MustParseAddr(tc.ip)); got != tc.want {
				t.Fatalf("isPublicIP(%s)=%v want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestValidTarget(t *testing.T) {
	for _, value := range []string{"example.com:443", "1.1.1.1:53", "[2606:4700:4700::1111]:443"} {
		if !validTarget(value) {
			t.Errorf("expected valid target %q", value)
		}
	}
	for _, value := range []string{"", "example.com", "localhost:abc", "example.com:65536", ":443"} {
		if validTarget(value) {
			t.Errorf("expected invalid target %q", value)
		}
	}
}