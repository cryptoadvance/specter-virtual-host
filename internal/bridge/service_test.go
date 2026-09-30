package bridge

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/gorilla/websocket"
)

type testWSFrame struct {
	wire []byte
	err  error
}

type testWSClient struct {
	conn    *websocket.Conn
	frames  chan testWSFrame
	current []byte
}

func newTestWebSocketPair(t *testing.T) (*wsConn, *testWSClient) {
	t.Helper()
	accepted := make(chan *wsConn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(maxFrame)
		accepted <- &wsConn{conn: conn}
	}))
	t.Cleanup(server.Close)
	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test WebSocket: %v", err)
	}
	client := &testWSClient{conn: clientConn, frames: make(chan testWSFrame, 64)}
	clientConn.SetPingHandler(func(data string) error {
		if err := clientConn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeTimeout)); err != nil {
			return err
		}
		client.frames <- testWSFrame{wire: unmaskedTestFrame(websocket.PingMessage, []byte(data))}
		return nil
	})
	clientConn.SetPongHandler(func(data string) error {
		client.frames <- testWSFrame{wire: unmaskedTestFrame(websocket.PongMessage, []byte(data))}
		return nil
	})
	t.Cleanup(func() { _ = clientConn.Close() })
	var serverConn *wsConn
	select {
	case serverConn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("test WebSocket server did not accept the client")
	}
	go func() {
		for {
			opcode, payload, err := clientConn.ReadMessage()
			if err != nil {
				client.frames <- testWSFrame{err: err}
				return
			}
			client.frames <- testWSFrame{wire: unmaskedTestFrame(opcode, payload)}
		}
	}()
	return serverConn, client
}

func (client *testWSClient) Read(payload []byte) (int, error) {
	for len(client.current) == 0 {
		frame := <-client.frames
		if frame.err != nil {
			return 0, frame.err
		}
		client.current = frame.wire
	}
	written := copy(payload, client.current)
	client.current = client.current[written:]
	return written, nil
}

func (client *testWSClient) Write(frame []byte) (int, error) {
	if len(frame) < 6 {
		return 0, io.ErrUnexpectedEOF
	}
	opcode := int(frame[0] & 0x0f)
	length := int(frame[1] & 0x7f)
	maskOffset := 2
	if length == 126 {
		if len(frame) < 8 {
			return 0, io.ErrUnexpectedEOF
		}
		length = int(binary.BigEndian.Uint16(frame[2:4]))
		maskOffset = 4
	}
	if frame[1]&0x80 == 0 || len(frame) < maskOffset+4+length {
		return 0, io.ErrUnexpectedEOF
	}
	mask := frame[maskOffset : maskOffset+4]
	data := append([]byte(nil), frame[maskOffset+4:maskOffset+4+length]...)
	for index := range data {
		data[index] ^= mask[index%4]
	}
	var err error
	if opcode == websocket.TextMessage || opcode == websocket.BinaryMessage {
		err = client.conn.WriteMessage(opcode, data)
	} else {
		err = client.conn.WriteControl(opcode, data, time.Now().Add(writeTimeout))
	}
	if err != nil {
		return 0, err
	}
	return len(frame), nil
}

func (client *testWSClient) Close() error { return client.conn.Close() }

func (client *testWSClient) SetReadDeadline(deadline time.Time) error {
	return client.conn.SetReadDeadline(deadline)
}

func unmaskedTestFrame(opcode int, payload []byte) []byte {
	frame := []byte{0x80 | byte(opcode)}
	switch {
	case len(payload) < 126:
		frame = append(frame, byte(len(payload)))
	case len(payload) <= 0xffff:
		frame = append(frame, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		frame = append(frame, 127, 0, 0, 0, 0, byte(len(payload)>>24), byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload)))
	}
	return append(frame, payload...)
}

type limitedTestWriter struct {
	bytes.Buffer
	maxWrite int
}

func (writer *limitedTestWriter) Write(payload []byte) (int, error) {
	if len(payload) > writer.maxWrite {
		payload = payload[:writer.maxWrite]
	}
	return writer.Buffer.Write(payload)
}

func TestWriteFullRetriesShortWrites(t *testing.T) {
	writer := &limitedTestWriter{maxWrite: 2}
	want := []byte("complete payload")
	if err := writeFull(writer, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(writer.Bytes(), want) {
		t.Fatalf("written payload = %q, want %q", writer.Bytes(), want)
	}
}

func maskedFrame(opcode byte, payload []byte) []byte {
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	frame := []byte{0x80 | opcode}
	if len(payload) < 126 {
		frame = append(frame, 0x80|byte(len(payload)))
	} else {
		frame = append(frame, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	}
	frame = append(frame, mask[:]...)
	for index, value := range payload {
		frame = append(frame, value^mask[index%4])
	}
	return frame
}

func readServerFrame(t *testing.T, reader *bufio.Reader) (byte, []byte) {
	t.Helper()
	first, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	length := int(second & 0x7f)
	if length == 126 {
		var size [2]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			t.Fatal(err)
		}
		length = int(binary.BigEndian.Uint16(size[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	return first & 0x0f, payload
}

func TestRequestOriginUsesOpenAndTrustedPolicy(t *testing.T) {
	settings := model.DefaultSettings()
	service := New("test", func() model.Settings { return settings }, nil)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8788/bridge", nil)
	request.Header.Set("Origin", "https://unlisted.example")
	if _, allowed := service.requestOrigin(request); !allowed {
		t.Fatal("open mode should allow an unlisted valid origin")
	}
	settings.OriginPolicy = model.OriginPolicyTrusted
	if _, allowed := service.requestOrigin(request); allowed {
		t.Fatal("trusted mode should reject an unlisted origin")
	}
	request.Header.Set("Origin", "https://try.clavastack.com")
	if _, allowed := service.requestOrigin(request); !allowed {
		t.Fatal("trusted mode should allow a built-in origin")
	}
}

func TestUnknownOriginWaitsForApprovalInTrustedMode(t *testing.T) {
	settings := model.DefaultSettings()
	settings.OriginPolicy = model.OriginPolicyTrusted
	service := New("test", func() model.Settings { return settings }, nil)
	result := make(chan bool, 1)
	go func() { result <- service.RequestApproval("https://unknown.example") }()

	var request model.PendingRequest
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests := service.PendingRequests()
		if len(requests) == 1 {
			request = requests[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if request.ID == "" || request.Origin != "https://unknown.example" {
		t.Fatalf("pending request = %+v", request)
	}
	if time.Until(request.ExpiresAt) < 25*time.Second || time.Until(request.ExpiresAt) > 30*time.Second {
		t.Fatalf("request timeout = %v", time.Until(request.ExpiresAt))
	}
	if _, found, err := service.ResolveRequest(request.ID, true, nil); err != nil || !found {
		t.Fatalf("resolve once: found=%v err=%v", found, err)
	}
	select {
	case allowed := <-result:
		if !allowed {
			t.Fatal("approved origin was denied")
		}
	case <-time.After(time.Second):
		t.Fatal("waiting bridge request was not released")
	}
	if len(service.PendingRequests()) != 0 {
		t.Fatal("resolved request is still pending")
	}
}

func TestOpenModeDoesNotCreateApprovalRequest(t *testing.T) {
	settings := model.DefaultSettings()
	service := New("test", func() model.Settings { return settings }, nil)
	if !service.RequestApproval("https://unknown.example") {
		t.Fatal("open origin policy should not block a valid origin")
	}
	if len(service.PendingRequests()) != 0 {
		t.Fatal("open mode should not create a connection request")
	}
}

func TestProxiedLoopbackUsesConfiguredUpstreamOrigin(t *testing.T) {
	settings := model.DefaultSettings()
	settings.OriginPolicy = model.OriginPolicyTrusted
	service := New("test", func() model.Settings { return settings }, nil)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8788/bridge", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8788")
	origin, allowed := service.requestOrigin(request)
	if !allowed || origin != "https://try.clavastack.com" {
		t.Fatalf("proxied origin = %q, allowed = %v", origin, allowed)
	}
}

func TestVirtualHostHealthUsesAllowedRequestOrigin(t *testing.T) {
	settings := model.DefaultSettings()
	settings.OriginPolicy = model.OriginPolicyTrusted
	service := New("test", func() model.Settings { return settings }, nil)
	for _, test := range []struct {
		origin string
		want   string
	}{
		{origin: "https://try.clavastack.com", want: "https://try.clavastack.com"},
		{origin: "https://untrusted.example", want: ""},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8788/virtual-host-health", nil)
		request.Header.Set("Origin", test.origin)
		service.serveHealth(recorder, request)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != test.want {
			t.Fatalf("origin %q: Access-Control-Allow-Origin = %q, want %q", test.origin, got, test.want)
		}
	}
}

func TestConnectedSimulatorRedirect(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	service.webAddress = "127.0.0.1:8788"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8788/connected?probe=usb", nil)
	service.serveConnected(recorder, request)
	response := recorder.Result()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusFound)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if location.Host != service.webAddress || query.Get("virtual-host") != "1" || query.Get("variant") != "diy" || query.Get("probe") != "usb" {
		t.Fatalf("location = %q", location)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header.Get("Cache-Control"))
	}
}

func TestOpenSimulatorDoesNotReplaceConnectedSession(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	service.running = true
	service.browser = &wsConn{}

	if err := service.OpenSimulator(); err == nil || err.Error() != "a simulator is already connected; use its existing browser tab to preserve its session" {
		t.Fatalf("OpenSimulator() error = %v, want connected-session warning", err)
	}
}

func TestProxyRewritesSimulatorRequestAndDisablesCaching(t *testing.T) {
	var received, receivedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.String()
		receivedHost = r.Host
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("ETag", "stale")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	settings := model.DefaultSettings()
	settings.Site = upstream.URL
	service := New("test", func() model.Settings { return settings }, nil)
	request := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1:8788/?virtual-host=1&variant=play&buildVariant=fast&manifest=/api/ab/pointer/expired", nil)
	response := httptest.NewRecorder()
	service.serveProxy(response, request)
	parsed, err := url.Parse(received)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/" || query.Get("virtual-host") != "1" || query.Get("variant") != "diy" || query.Has("manifest") || query.Has("buildVariant") {
		t.Fatalf("upstream URL = %q", received)
	}
	if receivedHost != upstream.Listener.Addr().String() {
		t.Fatalf("upstream Host = %q, want %q", receivedHost, upstream.Listener.Addr().String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" {
		t.Fatalf("cache headers = %v", response.Header())
	}
}

func TestBrowserSessionPromotesStandbyWhenActiveDisconnects(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	firstServer, firstClient := newTestWebSocketPair(t)
	defer firstClient.Close()
	go service.handleBrowser(firstServer, "first-tab", "https://first.example")
	firstReader := bufio.NewReader(firstClient)
	if opcode, _ := readServerFrame(t, firstReader); opcode != 1 {
		t.Fatalf("first hello opcode = %d, want text", opcode)
	}

	secondServer, secondClient := newTestWebSocketPair(t)
	defer secondClient.Close()
	go service.handleBrowser(secondServer, "second-tab", "https://second.example")
	secondReader := bufio.NewReader(secondClient)
	// A newer tab becomes active, but the old tab stays connected in standby.
	if opcode, _ := readServerFrame(t, firstReader); opcode != 1 {
		t.Fatalf("first-tab standby status opcode = %d, want hello", opcode)
	}
	if opcode, _ := readServerFrame(t, secondReader); opcode != 1 {
		t.Fatalf("second hello opcode = %d, want text", opcode)
	}
	if status := service.Status(); status.BrowserOrigin != "https://second.example" {
		t.Fatalf("active browser origin = %q, want newest tab", status.BrowserOrigin)
	}
	if _, err := secondClient.Write(maskedFrame(9, []byte("ping"))); err != nil {
		t.Fatalf("write active-tab ping: %v", err)
	}
	if opcode, _ := readServerFrame(t, secondReader); opcode != 10 {
		t.Fatalf("active-tab ping response opcode = %d, want pong", opcode)
	}

	_ = secondClient.Close()
	opcode, payload := readServerFrame(t, firstReader)
	if opcode != 1 {
		t.Fatalf("promoted tab frame opcode = %d, want hello", opcode)
	}
	var hello struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &hello); err != nil || hello.Type != "hello" {
		t.Fatalf("promoted tab hello = %q, err %v", payload, err)
	}
	if status := service.Status(); status.BrowserOrigin != "https://first.example" {
		t.Fatalf("promoted browser origin = %q, want first tab", status.BrowserOrigin)
	}
}

func TestCurrentBrowserBlocksLegacyReconnect(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	if !service.acceptsBrowserClient("") {
		t.Fatal("legacy browser should be accepted before a current page connects")
	}
	service.browserClientID = "current-tab"
	if service.acceptsBrowserClient("") {
		t.Fatal("legacy browser reconnect should be rejected after a current page connects")
	}
	if !service.acceptsBrowserClient("new-tab") {
		t.Fatal("a new current browser should be accepted")
	}
}

func TestBridgeRoundTrip(t *testing.T) {
	settings := model.DefaultSettings()
	service := NewWithAddresses("test", "127.0.0.1:0", "127.0.0.1:0", func() model.Settings { return settings }, nil)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	// Desktop may probe before the simulator has connected. That one short HWI
	// transaction is expected to fail; its next discovery probe must work once
	// the browser session is available.
	earlyClient, err := net.DialTimeout("tcp", service.hwiListener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("early HWI probe could not connect to listener: %v", err)
	}
	_, _ = earlyClient.Write([]byte("\r\n\r\nfingerprint\r\n"))
	_ = earlyClient.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := earlyClient.Read(make([]byte, 1)); err == nil {
		t.Fatal("HWI probe sent before a simulator was available should close without a response")
	}
	_ = earlyClient.Close()

	standbyServer, standbyClient := newTestWebSocketPair(t)
	defer standbyClient.Close()
	go service.handleBrowser(standbyServer, "first-tab", "https://example.com")
	standbyReader := bufio.NewReader(standbyClient)
	if opcode, _ := readServerFrame(t, standbyReader); opcode != 1 {
		t.Fatalf("hello opcode = %d", opcode)
	}
	browserServer, browserClient := newTestWebSocketPair(t)
	defer browserClient.Close()
	go service.handleBrowser(browserServer, "newest-tab", "https://example.com")
	browserReader := bufio.NewReader(browserClient)
	if opcode, _ := readServerFrame(t, standbyReader); opcode != 1 {
		t.Fatalf("standby update opcode = %d", opcode)
	}
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatalf("active hello opcode = %d", opcode)
	}

	hostClient, err := net.DialTimeout("tcp", service.hwiListener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("connect to HWI TCP listener: %v", err)
	}
	defer hostClient.Close()
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatalf("host status opcode = %d", opcode)
	}
	command := []byte("\r\n\r\nfingerprint\r\n")
	if _, err := hostClient.Write(command); err != nil {
		t.Fatalf("write HWI command: %v", err)
	}
	opcode, payload := readServerFrame(t, browserReader)
	if opcode != 2 || string(payload) != string(command) {
		t.Fatalf("host-to-browser = (%d, %q)", opcode, payload)
	}
	latestServer, latestClient := newTestWebSocketPair(t)
	defer latestClient.Close()
	go service.handleBrowser(latestServer, "latest-tab", "https://example.com")
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatalf("in-flight tab status opcode = %d", opcode)
	}
	latestReader := bufio.NewReader(latestClient)
	if opcode, _ := readServerFrame(t, latestReader); opcode != 1 {
		t.Fatalf("latest tab hello opcode = %d", opcode)
	}
	// Previous tabs cannot inject replies into the newer active HWI request.
	if _, err := standbyClient.Write(maskedFrame(2, []byte("ACK\r\nwrong\r\n"))); err != nil {
		t.Fatalf("write standby response: %v", err)
	}
	_ = hostClient.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := hostClient.Read(make([]byte, 1)); err == nil {
		t.Fatal("standby tab response reached HWI client")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("unexpected HWI read error: %v", err)
	}
	_ = hostClient.SetReadDeadline(time.Time{})
	response := []byte("ACK\r\ndeadbeef\r\n")
	if _, err := browserClient.Write(maskedFrame(2, response)); err != nil {
		t.Fatal(err)
	}
	_ = hostClient.SetReadDeadline(time.Now().Add(time.Second))
	received := make([]byte, len(response))
	if _, err := io.ReadFull(hostClient, received); err != nil {
		t.Fatal(err)
	}
	if string(received) != string(response) {
		t.Fatalf("browser-to-host = %q", received)
	}
}

func TestConcurrentHostConnectionsAreSerialized(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	service.mu.Lock()
	service.running = true
	service.generation = 1
	service.mu.Unlock()
	browserServer, browserClient := newTestWebSocketPair(t)
	defer browserClient.Close()
	go service.handleBrowser(browserServer, "test-tab", "https://example.com")
	browserReader := bufio.NewReader(browserClient)
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatalf("hello opcode = %d, want text", opcode)
	}
	browserClient.SetReadDeadline(time.Now().Add(2 * time.Second))

	firstServer, firstClient := net.Pipe()
	startHostHandlerForTest(service, firstServer)
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatalf("first host status opcode = %d, want text", opcode)
	}

	firstCommand := []byte("fingerprint\r\n")
	firstWrite := make(chan error, 1)
	go func() {
		_, err := firstClient.Write(firstCommand)
		firstWrite <- err
	}()
	opcode, payload := readServerFrame(t, browserReader)
	if opcode != 2 || string(payload) != string(firstCommand) {
		t.Fatalf("first host request = (%d, %q)", opcode, payload)
	}
	if err := <-firstWrite; err != nil {
		t.Fatalf("write first host request: %v", err)
	}

	secondServer, secondClient := net.Pipe()
	startHostHandlerForTest(service, secondServer)
	secondCommand := []byte("xpub m/84h/1h/0h\r\n")
	secondWrite := make(chan error, 1)
	go func() {
		_, err := secondClient.Write(secondCommand)
		secondWrite <- err
	}()

	firstResponse := []byte("ACK\r\n12345678\r\n")
	if _, err := browserClient.Write(maskedFrame(2, firstResponse)); err != nil {
		t.Fatalf("write first browser response: %v", err)
	}
	firstClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	firstReceived := make([]byte, len(firstResponse))
	if _, err := io.ReadFull(firstClient, firstReceived); err != nil {
		t.Fatalf("read first host response: %v", err)
	}
	if string(firstReceived) != string(firstResponse) {
		t.Fatalf("first host response = %q, want %q", firstReceived, firstResponse)
	}
	_ = firstClient.Close()
	defer secondClient.Close()

	var secondOpcode byte
	var secondPayload []byte
	for attempts := 0; attempts < 3; attempts++ {
		secondOpcode, secondPayload = readServerFrame(t, browserReader)
		if secondOpcode == 2 {
			break
		}
	}
	if secondOpcode != 2 || string(secondPayload) != string(secondCommand) {
		t.Fatalf("second host request = (%d, %q)", secondOpcode, secondPayload)
	}
	if err := <-secondWrite; err != nil {
		t.Fatalf("write second host request: %v", err)
	}

	secondResponse := []byte("ACK\r\nxpub-result\r\n")
	if _, err := browserClient.Write(maskedFrame(2, secondResponse)); err != nil {
		t.Fatalf("write second browser response: %v", err)
	}
	secondClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	secondReceived := make([]byte, len(secondResponse))
	if _, err := io.ReadFull(secondClient, secondReceived); err != nil {
		t.Fatalf("read second host response: %v", err)
	}
	if string(secondReceived) != string(secondResponse) {
		t.Fatalf("second host response = %q, want %q", secondReceived, secondResponse)
	}
}

func startHostHandlerForTest(service *Service, connection net.Conn) {
	service.mu.Lock()
	service.hostConnections[connection] = struct{}{}
	generation := service.generation
	service.mu.Unlock()
	go service.handleHost(connection, generation)
}

func waitForTestCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestInterruptedHWIResponseQuarantinesSimulatorUntilPageReload(t *testing.T) {
	recoveryEvents := make(chan string, 4)
	service := NewWithAddresses("test", "127.0.0.1:0", "127.0.0.1:0", func() model.Settings {
		return model.DefaultSettings()
	}, func(_, _, message, result string) {
		if result == "recovery-required" {
			recoveryEvents <- message
		}
	})
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	firstBrowserServer, firstBrowserClient := newTestWebSocketPair(t)
	firstBrowserReader := bufio.NewReader(firstBrowserClient)
	go service.handleBrowser(firstBrowserServer, "page-one", "https://example.com")
	if opcode, _ := readServerFrame(t, firstBrowserReader); opcode != 1 {
		t.Fatal("expected initial hello from first simulator page")
	}

	firstHost, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if opcode, _ := readServerFrame(t, firstBrowserReader); opcode != 1 {
		t.Fatal("expected host-connected status")
	}
	command := []byte("fingerprint\r\n")
	if _, err := firstHost.Write(command); err != nil {
		t.Fatal(err)
	}
	if opcode, payload := readServerFrame(t, firstBrowserReader); opcode != 2 || string(payload) != string(command) {
		t.Fatalf("first request = (%d, %q), want forwarded fingerprint", opcode, payload)
	}
	ack := []byte("ACK\r\n")
	if _, err := firstBrowserClient.Write(maskedFrame(2, ack)); err != nil {
		t.Fatal(err)
	}
	_ = firstHost.SetReadDeadline(time.Now().Add(time.Second))
	ackReceived := make([]byte, len(ack))
	if _, err := io.ReadFull(firstHost, ackReceived); err != nil {
		t.Fatal(err)
	}
	if string(ackReceived) != string(ack) {
		t.Fatalf("partial response ACK = %q, want %q", ackReceived, ack)
	}
	_ = firstHost.Close()

	// The client closed while the device command was still in flight. The
	// connection-status frame also lets the host handler release its session lock.
	if opcode, _ := readServerFrame(t, firstBrowserReader); opcode != 1 {
		t.Fatal("expected host-disconnected status")
	}
	waitForTestCondition(t, "the interrupted simulator client to be quarantined", func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		_, found := service.quarantinedBrowserClients["page-one"]
		return found
	})
	if status := service.Status(); !status.BrowserConnected || !status.BrowserRecoveryRequired {
		t.Fatalf("status after an interrupted response = %+v; want connected simulator with recovery required", status)
	}
	select {
	case message := <-recoveryEvents:
		if !bytes.Contains([]byte(message), []byte("5 response bytes across 1 line")) {
			t.Fatalf("recovery event = %q; want safe partial-response diagnostics", message)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted transaction did not produce a recovery-required activity event")
	}

	// A late answer from the old request is discarded while no HWI client owns
	// the simulator stream.
	if _, err := firstBrowserClient.Write(maskedFrame(2, []byte("old-fingerprint\r\n"))); err != nil {
		t.Fatal(err)
	}

	// The same page's auto-reconnected session must not service a new request:
	// its old command could still produce a late response on that stream.
	samePageHost, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = samePageHost.SetReadDeadline(time.Now().Add(time.Second))
	var discarded [1]byte
	if n, err := samePageHost.Read(discarded[:]); n != 0 || err == nil {
		t.Fatalf("same simulator page was not rejected: read %d bytes, err %v", n, err)
	}
	_ = samePageHost.Close()
	_ = firstBrowserClient.Close()

	// A page reload creates a new simulator client ID and a clean worker stream.
	secondBrowserServer, secondBrowserClient := newTestWebSocketPair(t)
	defer secondBrowserClient.Close()
	secondBrowserReader := bufio.NewReader(secondBrowserClient)
	go service.handleBrowser(secondBrowserServer, "page-two", "https://example.com")
	if opcode, _ := readServerFrame(t, secondBrowserReader); opcode != 1 {
		t.Fatal("expected hello from reloaded simulator page")
	}
	if status := service.Status(); !status.BrowserConnected || status.BrowserRecoveryRequired {
		t.Fatalf("status after simulator reload = %+v; want connected simulator ready for wallet requests", status)
	}

	secondHost, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer secondHost.Close()
	if opcode, _ := readServerFrame(t, secondBrowserReader); opcode != 1 {
		t.Fatal("expected host-connected status after simulator reload")
	}
	if _, err := secondHost.Write(command); err != nil {
		t.Fatal(err)
	}
	if opcode, payload := readServerFrame(t, secondBrowserReader); opcode != 2 || string(payload) != string(command) {
		t.Fatalf("reloaded simulator request = (%d, %q)", opcode, payload)
	}
	if _, err := secondBrowserClient.Write(maskedFrame(2, ack)); err != nil {
		t.Fatal(err)
	}
	_ = secondHost.SetReadDeadline(time.Now().Add(time.Second))
	ackReceived = make([]byte, len(ack))
	if _, err := io.ReadFull(secondHost, ackReceived); err != nil {
		t.Fatal(err)
	}
	if string(ackReceived) != string(ack) {
		t.Fatalf("reloaded simulator ACK = %q, want %q", ackReceived, ack)
	}
	response := []byte("new-fingerprint\r\n")
	if _, err := secondBrowserClient.Write(maskedFrame(2, response)); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, len(response))
	if _, err := io.ReadFull(secondHost, received); err != nil {
		t.Fatal(err)
	}
	if string(received) != string(response) {
		t.Fatalf("reloaded simulator response = %q, want %q", received, response)
	}
}

func TestIdleHWIConnectionCannotHoldSessionLockForever(t *testing.T) {
	service := NewWithAddresses("test", "127.0.0.1:0", "127.0.0.1:0", func() model.Settings {
		return model.DefaultSettings()
	}, nil)
	service.firstHostRequestTimeout = 100 * time.Millisecond
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	browserServer, browserClient := newTestWebSocketPair(t)
	defer browserClient.Close()
	browserReader := bufio.NewReader(browserClient)
	go service.handleBrowser(browserServer, "idle-test-page", "https://example.com")
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatal("expected initial simulator hello")
	}

	idleClient, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer idleClient.Close()
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatal("expected idle client connection status")
	}
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatal("idle client did not time out and release the HWI session")
	}
	_ = idleClient.SetReadDeadline(time.Now().Add(time.Second))
	var idleByte [1]byte
	if n, err := idleClient.Read(idleByte[:]); n != 0 || err == nil {
		t.Fatalf("idle HWI connection stayed open: read %d bytes, err %v", n, err)
	}

	client, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if opcode, _ := readServerFrame(t, browserReader); opcode != 1 {
		t.Fatal("expected connected status for the next HWI client")
	}
	command := []byte("fingerprint\r\n")
	if _, err := client.Write(command); err != nil {
		t.Fatal(err)
	}
	if opcode, payload := readServerFrame(t, browserReader); opcode != 2 || string(payload) != string(command) {
		t.Fatalf("next request = (%d, %q), want fingerprint", opcode, payload)
	}
	response := []byte("ACK\r\n12345678\r\n")
	if _, err := browserClient.Write(maskedFrame(2, response)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	received := make([]byte, len(response))
	if _, err := io.ReadFull(client, received); err != nil {
		t.Fatal(err)
	}
	if string(received) != string(response) {
		t.Fatalf("next response = %q, want %q", received, response)
	}
}

func TestStopStartRejectsHWIConnectionsAcceptedByPreviousGeneration(t *testing.T) {
	service := NewWithAddresses("test", "127.0.0.1:0", "127.0.0.1:0", func() model.Settings {
		return model.DefaultSettings()
	}, nil)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	firstBrowserServer, firstBrowserClient := newTestWebSocketPair(t)
	firstBrowserReader := bufio.NewReader(firstBrowserClient)
	go service.handleBrowser(firstBrowserServer, "old-page", "https://example.com")
	if opcode, _ := readServerFrame(t, firstBrowserReader); opcode != 1 {
		t.Fatal("expected initial hello from first simulator page")
	}

	firstHost, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if opcode, _ := readServerFrame(t, firstBrowserReader); opcode != 1 {
		t.Fatal("expected first host-connected status")
	}
	firstCommand := []byte("fingerprint\r\n")
	if _, err := firstHost.Write(firstCommand); err != nil {
		t.Fatal(err)
	}
	if opcode, _ := readServerFrame(t, firstBrowserReader); opcode != 2 {
		t.Fatal("expected first request to reach simulator")
	}

	queuedHost, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queuedHost.Write([]byte("xpub m/84h/1h/0h\r\n")); err != nil {
		t.Fatal(err)
	}
	waitForTestCondition(t, "both old-generation clients to be registered", func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.hostConnections) >= 2
	})

	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	_ = firstHost.Close()
	_ = queuedHost.SetReadDeadline(time.Now().Add(time.Second))
	var closedCheck [1]byte
	if n, err := queuedHost.Read(closedCheck[:]); n != 0 || err == nil {
		t.Fatalf("queued old-generation client remained open: read %d bytes, err %v", n, err)
	}
	_ = queuedHost.Close()
	_ = firstBrowserClient.Close()

	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	waitForTestCondition(t, "old-generation clients to be removed", func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.hostConnections) == 0
	})

	newBrowserServer, newBrowserClient := newTestWebSocketPair(t)
	defer newBrowserClient.Close()
	newBrowserReader := bufio.NewReader(newBrowserClient)
	go service.handleBrowser(newBrowserServer, "new-page", "https://example.com")
	if opcode, _ := readServerFrame(t, newBrowserReader); opcode != 1 {
		t.Fatal("expected hello from restarted simulator page")
	}
	newHost, err := net.Dial("tcp", service.hwiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer newHost.Close()
	if opcode, _ := readServerFrame(t, newBrowserReader); opcode != 1 {
		t.Fatal("expected new host-connected status")
	}
	if _, err := newHost.Write(firstCommand); err != nil {
		t.Fatal(err)
	}
	if opcode, payload := readServerFrame(t, newBrowserReader); opcode != 2 || string(payload) != string(firstCommand) {
		t.Fatalf("new-generation request = (%d, %q)", opcode, payload)
	}
}

func TestBrowserHeartbeatKeepsResponsiveSessionAlive(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	server, client := newTestWebSocketPair(t)
	defer client.Close()
	done := make(chan struct{})
	go func() {
		service.handleBrowserWithHeartbeat(server, "test-tab", "https://example.com", 10*time.Millisecond, 100*time.Millisecond)
		close(done)
	}()
	reader := bufio.NewReader(client)
	if opcode, _ := readServerFrame(t, reader); opcode != 1 {
		t.Fatalf("hello opcode = %d, want text", opcode)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	for index := 0; index < 3; index++ {
		opcode, payload := readServerFrame(t, reader)
		if opcode != 9 {
			t.Fatalf("heartbeat opcode = %d, want ping", opcode)
		}
		if _, err := client.Write(maskedFrame(10, payload)); err != nil {
			t.Fatalf("send heartbeat pong: %v", err)
		}
	}
	if !service.Status().BrowserConnected {
		t.Fatal("responsive browser was disconnected despite answering heartbeat pings")
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("browser handler did not exit after the connection closed")
	}
}

func TestBrowserHeartbeatDisconnectsUnresponsiveSession(t *testing.T) {
	service := New("test", func() model.Settings { return model.DefaultSettings() }, nil)
	server, client := newTestWebSocketPair(t)
	defer client.Close()
	done := make(chan struct{})
	go func() {
		service.handleBrowserWithHeartbeat(server, "test-tab", "https://example.com", 10*time.Millisecond, 80*time.Millisecond)
		close(done)
	}()
	reader := bufio.NewReader(client)
	if opcode, _ := readServerFrame(t, reader); opcode != 1 {
		t.Fatalf("hello opcode = %d, want text", opcode)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if opcode, _ := readServerFrame(t, reader); opcode != 9 {
		t.Fatalf("heartbeat opcode = %d, want ping", opcode)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("browser handler did not expire the unresponsive connection")
	}
	if service.Status().BrowserConnected {
		t.Fatal("unresponsive browser remained connected after the read deadline")
	}
}
