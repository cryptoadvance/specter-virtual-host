package policy

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
)

func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid origin %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("origin must use http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("origin must not contain user information")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("trusted sites must be origins without a path, query, or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("origin host is empty")
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return strings.ToLower(parsed.Scheme) + "://" + host, nil
}

func Allows(settings model.Settings, rawOrigin string) (string, bool) {
	origin, err := NormalizeOrigin(rawOrigin)
	if err != nil {
		return "", false
	}
	if settings.OriginPolicy == model.OriginPolicyOpen {
		return origin, true
	}
	for _, site := range settings.TrustedSites {
		if !site.Enabled {
			continue
		}
		normalized, err := NormalizeOrigin(site.Origin)
		if err == nil && normalized == origin {
			return origin, true
		}
	}
	return origin, false
}
