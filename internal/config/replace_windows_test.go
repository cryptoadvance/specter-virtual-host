//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFileReplacesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "config.tmp")
	destination := filepath.Join(directory, "config.json")
	if err := os.WriteFile(source, []byte("new config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new config" {
		t.Fatalf("replacement contents = %q, want %q", got, "new config")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists: err = %v", err)
	}
}
