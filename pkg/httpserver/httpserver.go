package httpserver

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"torrent-class/pkg/discovery"
	"torrent-class/pkg/utils"
)

func StartShareableServer(localIP string, httpPort int, port int, getMagnet func() string) string {
	httpAddr := fmt.Sprintf("http://%s:%d", localIP, httpPort)

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)

	dateStr := time.Now().Format("2006-01-02")
	shareDirName := fmt.Sprintf("shareable_%s", dateStr)
	sharePath := filepath.Join(exeDir, shareDirName)

	os.MkdirAll(sharePath, 0755)

	newExePath := filepath.Join(sharePath, exeName)
	if err := utils.CopyFile(exePath, newExePath); err != nil {
		log.Printf("Failed to copy binary to shareable folder: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(sharePath)))

	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		actualMagnet := getMagnet()
		if actualMagnet == "" {
			http.Error(w, "Torrent not ready", http.StatusServiceUnavailable)
			return
		}
		info := discovery.DiscoveryInfo{
			Magnet: actualMagnet,
			IP:     localIP,
			Port:   port,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})

	go func() {
		addr := fmt.Sprintf(":%d", httpPort)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return httpAddr
}
