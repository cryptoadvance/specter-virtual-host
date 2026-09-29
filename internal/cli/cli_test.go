package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryptoadvance/specter-virtual-host/internal/config"
)

func TestRunnerWritesStatusToStdoutAndErrorsToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := Runner{
		Version: "test", ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Stdout: &stdout, Stderr: &stderr,
		ControlAvailable: func(context.Context) bool { return false },
	}
	if code := runner.Run(context.Background(), []string{"status"}); code != ExitOK {
		t.Fatalf("status exit code = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Specter Virtual Host test") || !strings.Contains(stdout.String(), "Bridge:") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("status wrote to stderr: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runner.Run(context.Background(), []string{"unknown-command"}); code != ExitUsage {
		t.Fatalf("invalid command exit code = %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("invalid command streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStatusExplainsSimulatorRecoveryRequired(t *testing.T) {
	var stdout bytes.Buffer
	runner := Runner{Version: "test", Stdout: &stdout}
	snapshot := map[string]any{
		"version": "test",
		"mode":    "headless",
		"bridge": map[string]any{
			"running": true, "browserConnected": true, "browserRecoveryRequired": true,
			"browserOrigin": "https://try.clavastack.com", "walletAllowed": true,
			"webAddress": "127.0.0.1:8788", "hwiAddress": "127.0.0.1:8789",
		},
		"settings": map[string]any{"originPolicy": "trusted"},
	}
	if err := runner.print("status", snapshot, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "reload simulator tab to recover") ||
		!strings.Contains(stdout.String(), "Wallet request:    recovery required") {
		t.Fatalf("status did not explain simulator recovery: %q", stdout.String())
	}
}

func TestRunnerSettingsUseSharedConfigWhenAppIsStopped(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer
	runner := Runner{
		Version: "test", ConfigPath: configPath, Stdout: &stdout, Stderr: &stderr,
		ControlAvailable: func(context.Context) bool { return false },
	}
	if code := runner.Run(context.Background(), []string{"settings", "set", "origin-policy", "trusted"}); code != ExitOK {
		t.Fatalf("settings set exit code = %d; stderr=%q", code, stderr.String())
	}
	settings, err := config.Open(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Get().OriginPolicy != "trusted" {
		t.Fatalf("origin policy = %q", settings.Get().OriginPolicy)
	}
}

func TestRunnerCanStopWhenNoInstanceIsRunning(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := Runner{
		Version: "test", ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Stdout: &stdout, Stderr: &stderr,
		ControlAvailable: func(context.Context) bool { return false },
	}
	if code := runner.Run(context.Background(), []string{"bridge", "stop"}); code != ExitOK {
		t.Fatalf("bridge stop exit code = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"running": false`) {
		t.Fatalf("bridge stop did not report a stopped bridge: %q", stdout.String())
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := Runner{
		Version: "test", Stdout: &stdout, Stderr: &stderr,
		ControlAvailable: func(context.Context) bool { return false },
	}
	if code := runner.Run(context.Background(), []string{"help"}); code != ExitOK {
		t.Fatalf("help exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
		t.Fatalf("help streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRequestCommandParsing(t *testing.T) {
	command, args, jsonOutput, err := parse([]string{"requests", "allow-once", "abc123", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "requests.allow-once" || args["id"] != "abc123" || !jsonOutput {
		t.Fatalf("parsed = %q, %#v, json=%v", command, args, jsonOutput)
	}
}
