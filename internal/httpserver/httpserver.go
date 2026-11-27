package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"torrent-class/internal/discovery"
	"torrent-class/internal/utils"
)

// StartShareableServer starts an HTTP server serving the shareable_YYYY-MM-DD
// folder (containing a copy of the running binary) and an /info discovery endpoint.
// Non-fatal copy or server errors are reported via onError instead of writing to stderr.
func StartShareableServer(
	ctx context.Context,
	localIP string,
	httpPort int,
	bitPort int,
	getMagnet func() string,
	onError func(string),
) string {
	httpAddr := fmt.Sprintf("http://%s:%d", localIP, httpPort)

	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)

	dateStr := time.Now().Format("2006-01-02")
	shareDirName := fmt.Sprintf("shareable_%s", dateStr)
	sharePath := filepath.Join(exeDir, shareDirName)

	if err := os.MkdirAll(sharePath, 0755); err != nil {
		if onError != nil {
			onError(fmt.Sprintf("Failed to create shareable folder: %v", err))
		}
	} else {
		newExePath := filepath.Join(sharePath, exeName)
		if err := utils.CopyFile(exePath, newExePath); err != nil && onError != nil {
			onError(fmt.Sprintf("Failed to copy binary to shareable folder: %v", err))
		}
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
			Port:   bitPort,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if onError != nil {
				onError(fmt.Sprintf("HTTP server error on port %d: %v", httpPort, err))
			}
		}
	}()

	return httpAddr
}
