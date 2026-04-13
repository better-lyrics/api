package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetDBFileSizeMB(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "test.db")
		// Create a 1MB file
		data := make([]byte, 1024*1024)
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}

		sizeMB := getDBFileSizeMB(path)
		if sizeMB < 0.9 || sizeMB > 1.1 {
			t.Errorf("expected ~1.0 MB, got %.2f MB", sizeMB)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		sizeMB := getDBFileSizeMB("/nonexistent/path/db")
		if sizeMB != 0 {
			t.Errorf("expected 0 for missing file, got %.2f", sizeMB)
		}
	})
}

func TestRotateHeapProfiles(t *testing.T) {
	t.Run("no rotation needed when under limit", func(t *testing.T) {
		dir := t.TempDir()
		// Create 3 profiles (under maxHeapProfiles=10)
		for _, name := range []string{"heap_2026-01-01.pprof", "heap_2026-01-02.pprof", "heap_2026-01-03.pprof"} {
			os.WriteFile(filepath.Join(dir, name), []byte("data"), 0644)
		}

		rotateHeapProfiles(dir)

		entries, _ := os.ReadDir(dir)
		if len(entries) != 3 {
			t.Errorf("expected 3 files, got %d", len(entries))
		}
	})

	t.Run("rotates oldest when at limit", func(t *testing.T) {
		dir := t.TempDir()
		// Create exactly maxHeapProfiles files
		names := make([]string, maxHeapProfiles)
		for i := 0; i < maxHeapProfiles; i++ {
			names[i] = filepath.Join(dir, "heap_2026-01-"+string(rune('A'+i))+".pprof")
			os.WriteFile(names[i], []byte("data"), 0644)
		}

		rotateHeapProfiles(dir)

		entries, _ := os.ReadDir(dir)
		if len(entries) != maxHeapProfiles-1 {
			t.Errorf("expected %d files after rotation, got %d", maxHeapProfiles-1, len(entries))
		}

		// Oldest file should be gone
		if _, err := os.Stat(names[0]); !os.IsNotExist(err) {
			t.Error("expected oldest file to be deleted")
		}
	})

	t.Run("ignores non-pprof files", func(t *testing.T) {
		dir := t.TempDir()
		// Create maxHeapProfiles pprof files + 1 non-pprof file
		for i := 0; i < maxHeapProfiles; i++ {
			os.WriteFile(filepath.Join(dir, "heap_2026-01-"+string(rune('A'+i))+".pprof"), []byte("data"), 0644)
		}
		os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me"), 0644)

		rotateHeapProfiles(dir)

		// notes.txt should still exist
		if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
			t.Error("non-pprof file should not be deleted")
		}
	})

	t.Run("handles empty directory", func(t *testing.T) {
		dir := t.TempDir()
		rotateHeapProfiles(dir) // should not panic
	})

	t.Run("handles nonexistent directory", func(t *testing.T) {
		rotateHeapProfiles("/nonexistent/dir") // should not panic
	})
}
