//go:build windows

package control

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
)

func endpoint() string {
	root, _ := os.UserConfigDir()
	hash := sha256.Sum256([]byte(root))
	return fmt.Sprintf(`\\.\pipe\specter-virtual-host-%x`, hash[:8])
}

func listen() (net.Listener, error) {
	return winio.ListenPipe(endpoint(), &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;OW)",
		MessageMode:        false,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
}

func dial(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	dialContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return winio.DialPipeContext(dialContext, endpoint())
}

func cleanupEndpoint() {}
