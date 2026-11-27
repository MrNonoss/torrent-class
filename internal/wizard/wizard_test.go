package wizard

import "testing"

func TestIsValidIPv4(t *testing.T) {
	valid := []string{"192.168.1.10", "10.0.0.1", "172.16.5.20"}
	for _, ip := range valid {
		if !IsValidIPv4(ip) {
			t.Errorf("IsValidIPv4(%q) = false, want true", ip)
		}
	}

	invalid := []string{"", "192.168.1.10:8000", "::1", "2001:db8::1", "999.1.1.1", "not-an-ip"}
	for _, ip := range invalid {
		if IsValidIPv4(ip) {
			t.Errorf("IsValidIPv4(%q) = true, want false", ip)
		}
	}
}
