package activity

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
)

const maxEntries = 500

type Store struct {
	mu      sync.RWMutex
	path    string
	entries []model.Activity
}

func Open(configPath string) (*Store, error) {
	path := filepath.Join(filepath.Dir(configPath), "activity.jsonl")
	store := &Store{path: path}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		var entry model.Activity
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			store.entries = append(store.entries, entry)
		}
	}
	if len(store.entries) > maxEntries {
		store.entries = append([]model.Activity(nil), store.entries[len(store.entries)-maxEntries:]...)
	}
	return store, scanner.Err()
}

func (s *Store) Add(kind, origin, message, result string) model.Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	entry := model.Activity{
		ID: fmt.Sprintf("%d", now.UnixNano()), Time: now, Kind: kind,
		Origin: origin, Message: message, Result: result,
	}
	s.entries = append(s.entries, entry)
	if len(s.entries) > maxEntries {
		s.entries = append([]model.Activity(nil), s.entries[len(s.entries)-maxEntries:]...)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err == nil {
		if file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			data, _ := json.Marshal(entry)
			_, _ = file.Write(append(data, '\n'))
			_ = file.Close()
		}
	}
	return entry
}

func (s *Store) List() []model.Activity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := append([]model.Activity(nil), s.entries...)
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, nil, 0o600)
}
