package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Target struct {
	OS   string
	Arch string
}

func main() {
	targets := []Target{
		{"windows", "amd64"},
		{"linux", "amd64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
	}

	distName := "torrent-class"
	cmdPath := "./cmd/distributor"
	outputDir := "releases"

	// Ensure output directory exists
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		fmt.Printf("Creating %s directory...\n", outputDir)
		if err := os.Mkdir(outputDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("Starting build process...")

	for _, t := range targets {
		fileName := fmt.Sprintf("%s-%s-%s", distName, t.OS, t.Arch)
		if t.OS == "windows" {
			fileName += ".exe"
		}

		outputPath := filepath.Join(outputDir, fileName)
		fmt.Printf("Building for %s/%s -> %s\n", t.OS, t.Arch, outputPath)

		cmd := exec.Command("go", "build", "-o", outputPath, cmdPath)
		cmd.Env = append(os.Environ(),
			"GOOS="+t.OS,
			"GOARCH="+t.Arch,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error building for %s/%s: %v\n", t.OS, t.Arch, err)
			continue
		}
	}

	fmt.Println("Build process completed. Check the 'releases' folder.")
}
