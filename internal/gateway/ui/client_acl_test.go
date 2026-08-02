package ui

import (
	"net"
	"testing"
)

func TestIsAllowedUIClient(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.1.20", true},
		{"10.0.0.5", true},
		{"172.16.0.1", true},
		{"8.8.8.8", false},
		{"", false},
	}
	for _, tc := range cases {
		var ip net.IP
		if tc.ip != "" {
			ip = net.ParseIP(tc.ip)
		}
		if got := isAllowedUIClient(ip); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.ip, got, tc.want)
		}
	}
}
