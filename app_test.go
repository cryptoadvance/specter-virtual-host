package main

import (
	"context"
	"errors"
	"testing"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
)

type bridgeFailureExecutor struct{}

func (bridgeFailureExecutor) Execute(_ context.Context, command string, _ map[string]string) (any, error) {
	if command == "bridge.start" {
		return nil, errors.New("cannot open HWI endpoint 127.0.0.1:8789: address already in use")
	}
	return model.Snapshot{Settings: model.DefaultSettings()}, nil
}

func TestBridgeStartFailureDoesNotBlockStatus(t *testing.T) {
	app := newApplication("test")
	app.executor = bridgeFailureExecutor{}
	if _, err := app.Execute("bridge.start", nil); err == nil {
		t.Fatal("bridge.start should report its bind error")
	}

	result, err := app.Execute("status", nil)
	if err != nil {
		t.Fatalf("status should remain available after a bridge bind error: %v", err)
	}
	status, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("status result has type %T, want map[string]any", result)
	}
	bridge, ok := status["bridge"].(map[string]any)
	if !ok {
		t.Fatalf("bridge status has type %T, want map[string]any", status["bridge"])
	}
	if got := bridge["error"]; got != "cannot open HWI endpoint 127.0.0.1:8789: address already in use" {
		t.Fatalf("bridge error = %v", got)
	}
}
