package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/cryptoadvance/specter-virtual-host/internal/policy"
)

func TestSettingsAndSitesUseSharedCommandCore(t *testing.T) {
	instance, err := New("test", "offline", filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	ctx := context.Background()
	if _, err := instance.Execute(ctx, "settings.set", map[string]string{"key": "origin-policy", "value": "trusted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Execute(ctx, "sites.add", map[string]string{"origin": "https://Example.com:443"}); err != nil {
		t.Fatal(err)
	}
	snapshot := instance.Snapshot()
	if snapshot.Settings.OriginPolicy != "trusted" {
		t.Fatalf("origin policy = %q", snapshot.Settings.OriginPolicy)
	}
	found := false
	for _, site := range snapshot.Settings.TrustedSites {
		if site.Origin == "https://example.com" && site.Enabled {
			found = true
		}
	}
	if !found {
		t.Fatal("normalized trusted origin was not saved")
	}
}

func TestPermanentRequestApprovalUpdatesSharedTrustedSites(t *testing.T) {
	instance, err := New("test", "headless", filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	ctx := context.Background()
	if _, err := instance.Execute(ctx, "settings.set", map[string]string{"key": "origin-policy", "value": "trusted"}); err != nil {
		t.Fatal(err)
	}
	result := make(chan bool, 1)
	go func() { result <- instance.bridge.RequestApproval("https://new-simulator.example") }()

	var requestID string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests := instance.bridge.PendingRequests()
		if len(requests) > 0 {
			requestID = requests[0].ID
			break
		}
		time.Sleep(time.Millisecond)
	}
	if requestID == "" {
		t.Fatal("unknown origin did not create an approval request")
	}
	if _, err := instance.Execute(ctx, "requests.trust", map[string]string{"id": requestID}); err != nil {
		t.Fatal(err)
	}
	select {
	case allowed := <-result:
		if !allowed {
			t.Fatal("permanently trusted origin was denied")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request was not released")
	}
	allowedOrigin, allowed := policy.Allows(instance.Snapshot().Settings, "https://new-simulator.example")
	if !allowed || allowedOrigin != "https://new-simulator.example" {
		t.Fatal("permanent approval was not saved in the shared policy")
	}
}

func TestChangingPolicyResolvesPendingRequests(t *testing.T) {
	instance, err := New("test", "headless", filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	ctx := context.Background()
	if _, err := instance.Execute(ctx, "settings.set", map[string]string{"key": "origin-policy", "value": "trusted"}); err != nil {
		t.Fatal(err)
	}

	result := make(chan bool, 1)
	go func() { result <- instance.bridge.RequestApproval("https://pending.example") }()
	waitForRequest := func() string {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			requests := instance.bridge.PendingRequests()
			if len(requests) != 0 {
				return requests[0].ID
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("unknown origin did not create an approval request")
		return ""
	}
	_ = waitForRequest()
	if _, err := instance.Execute(ctx, "settings.set", map[string]string{"key": "origin-policy", "value": "open"}); err != nil {
		t.Fatal(err)
	}
	select {
	case allowed := <-result:
		if !allowed {
			t.Fatal("open policy did not release the pending website")
		}
	case <-time.After(time.Second):
		t.Fatal("open policy left the website request pending")
	}

	if _, err := instance.Execute(ctx, "settings.set", map[string]string{"key": "origin-policy", "value": "trusted"}); err != nil {
		t.Fatal(err)
	}
	deniedResult := make(chan bool, 1)
	go func() { deniedResult <- instance.bridge.RequestApproval("https://disabled-notifications.example") }()
	_ = waitForRequest()
	if _, err := instance.Execute(ctx, "settings.set", map[string]string{"key": "notify-new-site", "value": "false"}); err != nil {
		t.Fatal(err)
	}
	select {
	case allowed := <-deniedResult:
		if allowed {
			t.Fatal("disabling new-site notifications allowed a pending untrusted website")
		}
	case <-time.After(time.Second):
		t.Fatal("disabling new-site notifications left the website request pending")
	}
}

func TestMutationCommandsReturnUpdatedSnapshots(t *testing.T) {
	instance, err := NewWithAddresses("test", "offline", filepath.Join(t.TempDir(), "config.json"), "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	ctx := context.Background()

	started, err := instance.Execute(ctx, "bridge.start", nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := started.(model.Snapshot); !snapshot.Bridge.Running {
		t.Fatal("bridge.start returned a stopped snapshot")
	}

	cleared, err := instance.Execute(ctx, "log.clear", nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := cleared.(model.Snapshot); len(snapshot.Activity) != 0 {
		t.Fatal("log.clear returned activity that should have been cleared")
	}

	stopped, err := instance.Execute(ctx, "bridge.stop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := stopped.(model.Snapshot); snapshot.Bridge.Running {
		t.Fatal("bridge.stop returned a running snapshot")
	}
}
