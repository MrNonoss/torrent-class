//go:build ignore

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Target struct {
	OS   string
	Arch string
}

func main() {
	versionFlag := flag.String("version", "", "Release version string injected into main.version")
	outDirFlag := flag.String("out", "releases", "Output directory for built binaries")
	flag.Parse()

	version := resolveVersion(*versionFlag)

	targets := []Target{
		{"windows", "amd64"},
		{"windows", "arm64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
	}

	distName := "torrent-class"
	cmdPath := "./cmd/torrent-class"
	outputDir := *outDirFlag

	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		fmt.Printf("Creating %s directory...\n", outputDir)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Starting build process (version=%s)...\n", version)

	ldflags := fmt.Sprintf("-s -w -X main.version=%s", version)
	failed := false

	for _, t := range targets {
		fileName := fmt.Sprintf("%s-%s-%s", distName, t.OS, t.Arch)
		if t.OS == "windows" {
			fileName += ".exe"
		}

		outputPath := filepath.Join(outputDir, fileName)
		fmt.Printf("Building for %s/%s -> %s\n", t.OS, t.Arch, outputPath)

		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", outputPath, cmdPath)
		cmd.Env = append(os.Environ(),
			"CGO_ENABLED=0",
			"GOOS="+t.OS,
			"GOARCH="+t.Arch,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error building for %s/%s: %v\n", t.OS, t.Arch, err)
			failed = true
			continue
		}
	}

	if failed {
		os.Exit(1)
	}
	fmt.Println("Build process completed. Check the '" + outputDir + "' folder.")
}

func resolveVersion(explicit string) string {
	if explicit != "" {
		return strings.TrimPrefix(explicit, "v")
	}
	if env := os.Getenv("VERSION"); env != "" {
		return strings.TrimPrefix(env, "v")
	}
	if out, err := exec.Command("git", "describe", "--tags", "--exact-match").Output(); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	}
	return "2.6.0"
}
