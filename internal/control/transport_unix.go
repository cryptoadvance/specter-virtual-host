//go:build !windows

package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func endpoint() string {
	if runtimeDirectory := os.Getenv("XDG_RUNTIME_DIR"); runtimeDirectory != "" {
		return filepath.Join(runtimeDirectory, "specter-virtual-host.sock")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), fmt.Sprintf("specter-virtual-host-%d.sock", os.Getuid()))
	}
	return filepath.Join(cache, "specter-virtual-host", "control.sock")
}

func listen() (net.Listener, error) {
	path := endpoint()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if connection, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
		_ = connection.Close()
		return nil, fmt.Errorf("Specter Virtual Host is already running")
	}
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return listener, nil
}

func dial(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, "unix", endpoint())
}

func cleanupEndpoint() { _ = os.Remove(endpoint()) }
