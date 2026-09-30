package control

import (
	"net"
	"testing"
	"time"
)

type gatedListener struct {
	connection net.Conn
	entered    chan struct{}
	release    chan struct{}
	accepted   bool
}

func (listener *gatedListener) Accept() (net.Conn, error) {
	if listener.accepted {
		return nil, net.ErrClosed
	}
	listener.accepted = true
	close(listener.entered)
	<-listener.release
	return listener.connection, nil
}

func (listener *gatedListener) Close() error { return nil }

func (listener *gatedListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestAcceptLoopUsesStableListenerWhileServerCloses(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	client, accepted := net.Pipe()
	defer client.Close()
	listener := &gatedListener{
		connection: accepted,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	server := &Server{listener: listener}
	done := make(chan struct{})
	go func() {
		server.accept(listener)
		close(done)
	}()
	<-listener.entered

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	close(listener.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accept loop did not exit after its listener was closed")
	}
}
