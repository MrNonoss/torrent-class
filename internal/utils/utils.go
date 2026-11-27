package utils

import (
	"fmt"
	"io"
	"os"
)

// HumanizeBytes converts a byte count to a human-readable string.
func HumanizeBytes(b int64) string {
	if b < 0 {
		b = 0
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// CopyFile copies a file from src to dst and marks it executable.
func CopyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err = io.Copy(destination, source); err != nil {
		destination.Close()
		return err
	}
	if err = destination.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0755)
}
