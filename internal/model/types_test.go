package model

import "testing"

func TestDefaultSettingsStartBridgeWithoutOpeningAnotherSimulatorTab(t *testing.T) {
	settings := DefaultSettings()
	if !settings.StartBridgeOnLaunch {
		t.Fatal("bridge should start by default")
	}
	if settings.OpenSimulatorOnLaunch {
		t.Fatal("simulator should not open a new tab by default")
	}
	if settings.OriginPolicy != OriginPolicyOpen {
		t.Fatalf("origin policy = %q, want %q", settings.OriginPolicy, OriginPolicyOpen)
	}
}
