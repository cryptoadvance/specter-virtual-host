package activity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
)

func TestAddKeepsActivityFileAtMostMaxEntries(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxEntries+7; index++ {
		store.Add("website", "https://example.com", fmt.Sprintf("event-%d", index), "allowed")
	}

	entries := readActivityFile(t, filepath.Join(filepath.Dir(configPath), "activity.jsonl"))
	if len(entries) != maxEntries {
		t.Fatalf("persisted entry count = %d, want %d", len(entries), maxEntries)
	}
	if entries[0].Message != "event-7" || entries[len(entries)-1].Message != fmt.Sprintf("event-%d", maxEntries+6) {
		t.Fatalf("persisted range = %q..%q, want event-7..event-%d", entries[0].Message, entries[len(entries)-1].Message, maxEntries+6)
	}

	reopened, err := Open(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.List()); got != maxEntries {
		t.Fatalf("reopened entry count = %d, want %d", got, maxEntries)
	}
}

func TestOpenCompactsExistingOversizedActivityFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	logPath := filepath.Join(filepath.Dir(configPath), "activity.jsonl")
	file, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxEntries+3; index++ {
		entry := model.Activity{ID: fmt.Sprintf("%d", index), Message: fmt.Sprintf("event-%d", index)}
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(readActivityFile(t, logPath)); got != maxEntries {
		t.Fatalf("compacted entry count = %d, want %d", got, maxEntries)
	}
	entries := store.List()
	if entries[0].Message != fmt.Sprintf("event-%d", maxEntries+2) {
		t.Fatalf("newest loaded event = %q", entries[0].Message)
	}
}

func readActivityFile(t *testing.T, path string) []model.Activity {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var entries []model.Activity
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry model.Activity
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}
