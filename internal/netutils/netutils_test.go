package netutils

import (
	"net"
	"testing"
)

func TestGetBroadcastAddr(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"192.168.1.42/24", "192.168.1.255"},
		{"10.0.5.10/16", "10.0.255.255"},
		{"172.16.4.5/22", "172.16.7.255"},
	}

	for _, tc := range tests {
		ip, ipnet, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Fatal(err)
		}
		ipnet.IP = ip
		if got := GetBroadcastAddr(ipnet); got != tc.want {
			t.Errorf("GetBroadcastAddr(%s) = %s, want %s", tc.cidr, got, tc.want)
		}
	}
}

func TestSortInterfacesPrioritizesPhysicalPrivate(t *testing.T) {
	ifaces := []InterfaceInfo{
		{Name: "eth0", IP: "8.8.8.8"},
		{Name: "Tailscale", IP: "100.64.0.1"},
		{Name: "vEthernet (WSL)", IP: "172.24.0.1"},
		{Name: "Wi-Fi", IP: "192.168.1.50"},
	}

	sortInterfaces(ifaces)

	if ifaces[0].Name != "Wi-Fi" {
		t.Fatalf("expected Wi-Fi first, got %+v", ifaces)
	}
	if ifaces[len(ifaces)-1].Name != "Tailscale" {
		t.Fatalf("expected virtual non-private Tailscale last, got %+v", ifaces)
	}
}

func TestIsVirtualAdapter(t *testing.T) {
	virtual := []string{"vEthernet (Default Switch)", "docker0", "VMware Network Adapter", "Tailscale", "utun3", "tun0", "ZeroTier One"}
	for _, name := range virtual {
		if !IsVirtualAdapter(name) {
			t.Errorf("expected %q to be detected as virtual", name)
		}
	}

	physical := []string{"Wi-Fi", "Ethernet", "en0", "wlan0", "eth0"}
	for _, name := range physical {
		if IsVirtualAdapter(name) {
			t.Errorf("expected %q not to be detected as virtual", name)
		}
	}
}
