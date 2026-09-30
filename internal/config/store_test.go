package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
)

func TestOpenMergesBuiltInTrustedSitesWithSavedSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"version":1,"trustedSites":[{"origin":"https://try.clavastack.com/","enabled":false},{"origin":"https://custom.example","enabled":true}]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sites := store.Get().TrustedSites
	wantOrigins := map[string]bool{
		"https://try.clavastack.com":      false,
		"https://cryptoadvance.github.io": true,
		"https://custom.example":          true,
	}
	if len(sites) != len(wantOrigins) {
		t.Fatalf("got %d trusted sites, want %d: %#v", len(sites), len(wantOrigins), sites)
	}
	for _, site := range sites {
		wantEnabled, ok := wantOrigins[site.Origin]
		if !ok {
			t.Fatalf("unexpected trusted site %q", site.Origin)
		}
		if site.Enabled != wantEnabled {
			t.Errorf("site %q enabled = %t, want %t", site.Origin, site.Enabled, wantEnabled)
		}
		if site.Origin != "https://custom.example" && !site.BuiltIn {
			t.Errorf("default site %q is not marked built-in", site.Origin)
		}
	}
	if !containsOrigin(sites, "https://cryptoadvance.github.io") {
		t.Fatal("missing built-in site when a custom site already exists")
	}
}

func TestEmptyTrustedSiteListUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"trustedSites":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(store.Get().TrustedSites), len(model.DefaultSettings().TrustedSites); got != want {
		t.Fatalf("got %d default trusted sites, want %d", got, want)
	}
}

func containsOrigin(sites []model.TrustedSite, origin string) bool {
	for _, site := range sites {
		if site.Origin == origin {
			return true
		}
	}
	return false
}
