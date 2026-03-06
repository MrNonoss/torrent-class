package engine

import (
	"testing"
)

func TestCalculatePieceLength(t *testing.T) {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)

	tests := []struct {
		name      string
		totalSize int64
		want      int64
	}{
		{"Small (100MB)", 100 * MiB, 256 * KiB},
		{"Threshold 512MB", 511 * MiB, 256 * KiB},
		{"Switch to 512KB (750MB)", 750 * MiB, 512 * KiB},
		{"Threshold 1GB", 1023 * MiB, 512 * KiB},
		{"Switch to 1MB (1.5GB)", 1500 * MiB, 1 * MiB},
		{"Switch to 2MB (3GB)", 3 * GiB, 2 * MiB},
		{"Switch to 4MB (6GB)", 6 * GiB, 4 * MiB},
		{"Switch to 8MB (12GB)", 12 * GiB, 8 * MiB},
		{"Switch to 16MB (32GB)", 32 * GiB, 16 * MiB},
		{"Large (100GB)", 100 * GiB, 16 * MiB},
		{"Very Large (500GB)", 500 * GiB, 16 * MiB},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculatePieceLength(tt.totalSize); got != tt.want {
				t.Errorf("calculatePieceLength(%v) = %v, want %v", tt.totalSize, got, tt.want)
			}
		})
	}
}
