package policy

import (
	"testing"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
)

func TestNormalizeOrigin(t *testing.T) {
	tests := map[string]string{
		"https://Example.COM/":    "https://example.com",
		"https://example.com:443": "https://example.com",
		"http://localhost:8765":   "http://localhost:8765",
		"http://[::1]:8788":       "http://[::1]:8788",
	}
	for input, expected := range tests {
		actual, err := NormalizeOrigin(input)
		if err != nil {
			t.Fatalf("NormalizeOrigin(%q): %v", input, err)
		}
		if actual != expected {
			t.Fatalf("NormalizeOrigin(%q) = %q, want %q", input, actual, expected)
		}
	}
	for _, input := range []string{"", "null", "file:///tmp/test", "https://example.com/path", "https://user@example.com"} {
		if _, err := NormalizeOrigin(input); err == nil {
			t.Fatalf("NormalizeOrigin(%q) unexpectedly succeeded", input)
		}
	}
}

func TestPolicyOpenAndTrusted(t *testing.T) {
	settings := model.DefaultSettings()
	if _, allowed := Allows(settings, "https://unlisted.example"); !allowed {
		t.Fatal("open policy should allow a valid unlisted origin")
	}
	settings.OriginPolicy = model.OriginPolicyTrusted
	if _, allowed := Allows(settings, "https://try.clavastack.com"); !allowed {
		t.Fatal("trusted policy should allow an enabled built-in origin")
	}
	if _, allowed := Allows(settings, "https://unlisted.example"); allowed {
		t.Fatal("trusted policy should reject an unlisted origin")
	}
	settings.TrustedSites[0].Enabled = false
	if _, allowed := Allows(settings, "https://try.clavastack.com"); allowed {
		t.Fatal("trusted policy should reject a disabled origin")
	}
}
