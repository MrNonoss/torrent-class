package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
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

func TestCacheMatchesFilesDetectsRename(t *testing.T) {
	root := t.TempDir()
	fileA := filepath.Join(root, "a.txt")
	if err := os.WriteFile(fileA, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	info := metainfo.Info{
		Name: filepath.Base(root),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.txt"}, Length: 5},
		},
	}

	if !cacheMatchesFiles(info, root, true, []string{fileA}, 5) {
		t.Fatal("expected cacheMatchesFiles to match original file")
	}

	// Rename a.txt -> b.txt while preserving file count, size, and mtime.
	fileB := filepath.Join(root, "b.txt")
	if err := os.Rename(fileA, fileB); err != nil {
		t.Fatal(err)
	}

	if cacheMatchesFiles(info, root, true, []string{fileB}, 5) {
		t.Fatal("expected cacheMatchesFiles to invalidate after file rename")
	}
}

func TestLoopbackSeedAndDownload(t *testing.T) {
	seedParent := t.TempDir()
	seedFolder := filepath.Join(seedParent, "lab_files")
	if err := os.MkdirAll(filepath.Join(seedFolder, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	payload1 := bytes.Repeat([]byte("torrent-class-test-data-"), 2000)
	payload2 := []byte("nested file content for verification\n")

	if err := os.WriteFile(filepath.Join(seedFolder, "dataset.bin"), payload1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedFolder, "sub", "notes.txt"), payload2, 0644); err != nil {
		t.Fatal(err)
	}

	seedCfg := DefaultConfig(seedParent, 0, 20)
	seedCfg.ListenHost = torrent.LoopbackListenHost
	seedCfg.DisableTrackers = true
	seedStorage := storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   seedParent,
		PieceCompletion: storage.NewMapPieceCompletion(),
	})
	seedCfg.DefaultStorage = seedStorage
	seeder, err := NewEngineWithConfig(seedCfg)
	if err != nil {
		t.Fatalf("NewEngine(seeder): %v", err)
	}
	defer func() {
		seeder.Close()
		<-seeder.Client.Closed()
		_ = seedStorage.Close()
		runtime.GC()
	}()

	seedTorrent, skipped, err := seeder.CreateTorrentFromPath(seedFolder)
	if err != nil {
		t.Fatalf("CreateTorrentFromPath: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped files: %v", skipped)
	}
	<-seedTorrent.GotInfo()

	magnet := seeder.GetMagnetLink(seedTorrent)

	dlDir := t.TempDir()
	leechCfg := DefaultConfig(dlDir, 0, 20)
	leechCfg.ListenHost = torrent.LoopbackListenHost
	leechCfg.DisableTrackers = true
	leechStorage := storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   dlDir,
		PieceCompletion: storage.NewMapPieceCompletion(),
	})
	leechCfg.DefaultStorage = leechStorage
	leecher, err := NewEngineWithConfig(leechCfg)
	if err != nil {
		t.Fatalf("NewEngine(leecher): %v", err)
	}
	defer func() {
		leecher.Close()
		<-leecher.Client.Closed()
		_ = leechStorage.Close()
		runtime.GC()
	}()

	dlTorrent, err := leecher.AddTorrentByMagnet(magnet)
	if err != nil {
		t.Fatalf("AddTorrentByMagnet: %v", err)
	}
	t.Logf("seederAddrs=%v leecherAddrs=%v", seeder.Client.ListenAddrs(), leecher.Client.ListenAddrs())
	_ = leecher.AddPeer(dlTorrent, "127.0.0.1", seeder.Client.LocalPort())

	select {
	case <-dlTorrent.GotInfo():
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for torrent metadata; leecherStats=%+v seederStats=%+v", dlTorrent.Stats(), seedTorrent.Stats())
	}
	dlTorrent.DownloadAll()

	deadline := time.Now().Add(15 * time.Second)
	for dlTorrent.BytesMissing() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for download completion (missing %d bytes)", dlTorrent.BytesMissing())
		}
		time.Sleep(50 * time.Millisecond)
	}

	got1, err := os.ReadFile(filepath.Join(dlDir, "lab_files", "dataset.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got1, payload1) {
		t.Fatal("downloaded dataset.bin content mismatch")
	}

	got2, err := os.ReadFile(filepath.Join(dlDir, "lab_files", "sub", "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got2, payload2) {
		t.Fatal("downloaded sub/notes.txt content mismatch")
	}
}
