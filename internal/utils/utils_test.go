package utils

import "testing"

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{-5, "0 B"},
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tc := range tests {
		if got := HumanizeBytes(tc.in); got != tc.want {
			t.Errorf("HumanizeBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
