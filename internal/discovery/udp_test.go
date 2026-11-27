package discovery

import "testing"

func TestParseAnnouncement(t *testing.T) {
	validMagnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=LabVM"

	tests := []struct {
		name     string
		payload  string
		sourceIP string
		wantOK   bool
		wantIP   string
		wantPort int
	}{
		{
			name:     "valid payload uses packet source IP",
			payload:  MagicPrefix + validMagnet + "|172.20.10.5|8081",
			sourceIP: "192.168.1.42",
			wantOK:   true,
			wantIP:   "192.168.1.42",
			wantPort: 8081,
		},
		{
			name:     "falls back to payload IP when sourceIP empty",
			payload:  MagicPrefix + validMagnet + "|192.168.1.10|8081",
			sourceIP: "",
			wantOK:   true,
			wantIP:   "192.168.1.10",
			wantPort: 8081,
		},
		{
			name:     "rejects APIPA address when no valid sourceIP",
			payload:  MagicPrefix + validMagnet + "|169.254.12.34|8081",
			sourceIP: "",
			wantOK:   false,
		},
		{
			name:     "rejects wrong prefix",
			payload:  "OTHER:" + validMagnet + "|192.168.1.10|8081",
			sourceIP: "192.168.1.10",
			wantOK:   false,
		},
		{
			name:     "rejects missing magnet",
			payload:  MagicPrefix + "|192.168.1.10|8081",
			sourceIP: "192.168.1.10",
			wantOK:   false,
		},
		{
			name:     "rejects invalid port",
			payload:  MagicPrefix + validMagnet + "|192.168.1.10|99999",
			sourceIP: "192.168.1.10",
			wantOK:   false,
		},
		{
			name:     "rejects malformed field count",
			payload:  MagicPrefix + validMagnet + "|192.168.1.10",
			sourceIP: "192.168.1.10",
			wantOK:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseAnnouncement([]byte(tc.payload), tc.sourceIP)
			if ok != tc.wantOK {
				t.Fatalf("ParseAnnouncement() ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.IP != tc.wantIP || got.Port != tc.wantPort || got.Magnet != validMagnet {
				t.Fatalf("got %+v, want IP=%s Port=%d Magnet=%s", got, tc.wantIP, tc.wantPort, validMagnet)
			}
		})
	}
}
