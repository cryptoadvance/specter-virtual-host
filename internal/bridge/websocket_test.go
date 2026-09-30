package bridge

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/gorilla/websocket"
)

func TestBridgeLimitsConcurrentWebsiteConnectionsToFour(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	server := httptest.NewServer(service.routes())
	t.Cleanup(server.Close)
	defer service.Stop()

	type dialResult struct {
		conn     *websocket.Conn
		response *http.Response
		err      error
	}
	const attempts = 8
	results := make(chan dialResult, attempts)
	var wait sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			header := http.Header{"Origin": []string{fmt.Sprintf("https://site-%d.example", index)}}
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/bridge?client=client-%d", index)
			conn, response, err := websocket.DefaultDialer.Dial(endpoint, header)
			results <- dialResult{conn: conn, response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)
	var connected []*websocket.Conn
	for result := range results {
		if result.err == nil {
			connected = append(connected, result.conn)
			continue
		}
		if result.response == nil || result.response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("overflow connection error = %v, response = %v; want HTTP 429", result.err, result.response)
		}
	}
	t.Cleanup(func() {
		for _, conn := range connected {
			_ = conn.Close()
		}
	})
	if len(connected) != maxBrowserConnections {
		t.Fatalf("accepted %d simultaneous websites, want %d", len(connected), maxBrowserConnections)
	}
	waitForBrowserSessions(t, service, maxBrowserConnections)

	_ = connected[0].Close()
	waitForBrowserSessions(t, service, maxBrowserConnections-1)
	header := http.Header{"Origin": []string{"https://replacement.example"}}
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/bridge?client=replacement"
	replacement, response, err := websocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		t.Fatalf("connection after a website disconnected failed (response %v): %v", response, err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	waitForBrowserSessions(t, service, maxBrowserConnections)
}

func TestBridgeRejectsMalformedAndOversizedWebSocketMessages(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	server := httptest.NewServer(service.routes())
	t.Cleanup(server.Close)
	defer service.Stop()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/bridge?client=malformed-test"
	header := http.Header{"Origin": []string{"https://simulator.example"}}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	waitForBrowserSessions(t, service, 1)
	if _, err := conn.UnderlyingConn().Write([]byte{0x82, 0x01, 'x'}); err != nil {
		t.Fatalf("send malformed unmasked client frame: %v", err)
	}
	waitForBrowserSessions(t, service, 0)

	endpoint = "ws" + strings.TrimPrefix(server.URL, "http") + "/bridge?client=oversized-test"
	conn, _, err = websocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	waitForBrowserSessions(t, service, 1)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, maxFrame+1)); err != nil {
		t.Fatalf("send oversized binary message: %v", err)
	}
	waitForBrowserSessions(t, service, 0)
}

func waitForBrowserSessions(t *testing.T, service *Service, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		got := len(service.browserSessions)
		service.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	service.mu.Lock()
	got := len(service.browserSessions)
	service.mu.Unlock()
	t.Fatalf("active website connections = %d, want %d", got, want)
}
