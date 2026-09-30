package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/cryptoadvance/specter-virtual-host/internal/policy"
)

type Store struct {
	mu       sync.RWMutex
	path     string
	settings model.Settings
}

func DefaultPath() (string, error) {
	if configured := os.Getenv("SPECTER_VIRTUAL_HOST_CONFIG"); configured != "" {
		return filepath.Clean(configured), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "specter-virtual-host", "config.json"), nil
}

func Open(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	store := &Store{path: path, settings: model.DefaultSettings()}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &store.settings); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	store.applyDefaults()
	return store, nil
}

func (s *Store) applyDefaults() {
	defaults := model.DefaultSettings()
	if s.settings.Version == 0 {
		s.settings.Version = defaults.Version
	}
	if s.settings.Site == "" {
		s.settings.Site = defaults.Site
	}
	if s.settings.OriginPolicy == "" {
		s.settings.OriginPolicy = defaults.OriginPolicy
	}
	defaultSites := make(map[string]model.TrustedSite, len(defaults.TrustedSites))
	for _, site := range defaults.TrustedSites {
		defaultSites[site.Origin] = site
	}
	knownOrigins := make(map[string]struct{}, len(s.settings.TrustedSites))
	for index := range s.settings.TrustedSites {
		if origin, err := policy.NormalizeOrigin(s.settings.TrustedSites[index].Origin); err == nil {
			s.settings.TrustedSites[index].Origin = origin
			if _, isDefault := defaultSites[origin]; isDefault {
				s.settings.TrustedSites[index].BuiltIn = true
			}
			knownOrigins[origin] = struct{}{}
		}
	}
	for _, site := range defaults.TrustedSites {
		if _, exists := knownOrigins[site.Origin]; exists {
			continue
		}
		s.settings.TrustedSites = append(s.settings.TrustedSites, site)
		knownOrigins[site.Origin] = struct{}{}
	}
}

func (s *Store) Path() string { return s.path }

func (s *Store) Get() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := s.settings
	settings.TrustedSites = append([]model.TrustedSite(nil), s.settings.TrustedSites...)
	return settings
}

func (s *Store) Update(change func(*model.Settings) error) (model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.settings
	next.TrustedSites = append([]model.TrustedSite(nil), s.settings.TrustedSites...)
	if err := change(&next); err != nil {
		return model.Settings{}, err
	}
	if next.OriginPolicy != model.OriginPolicyOpen && next.OriginPolicy != model.OriginPolicyTrusted {
		return model.Settings{}, fmt.Errorf("origin policy must be open or trusted")
	}
	for index := range next.TrustedSites {
		normalized, err := policy.NormalizeOrigin(next.TrustedSites[index].Origin)
		if err != nil {
			return model.Settings{}, err
		}
		next.TrustedSites[index].Origin = normalized
	}
	if err := s.saveLocked(next); err != nil {
		return model.Settings{}, err
	}
	s.settings = next
	return s.GetUnsafe(), nil
}

func (s *Store) GetUnsafe() model.Settings {
	settings := s.settings
	settings.TrustedSites = append([]model.TrustedSite(nil), s.settings.TrustedSites...)
	return settings
}

func (s *Store) saveLocked(settings model.Settings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), "config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, s.path)
}
